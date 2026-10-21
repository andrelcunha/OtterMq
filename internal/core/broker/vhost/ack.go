package vhost

import (
	"fmt"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/rs/zerolog/log"
)

// unknownDeliveryTagError reports a delivery tag the channel has no outstanding
// delivery for, as required by basic.ack/nack/reject: the server must raise a
// channel exception. Clients rely on this to catch double-acks.
func unknownDeliveryTagError(deliveryTag uint64, methodID uint16) error {
	return errors.NewChannelError(
		fmt.Sprintf("PRECONDITION_FAILED - unknown delivery tag %d", deliveryTag),
		uint16(amqp.PRECONDITION_FAILED),
		uint16(amqp.BASIC),
		methodID,
	)
}

func (vh *VHost) HandleBasicAck(connID ConnectionID, channel uint16, deliveryTag uint64, multiple bool) error {
	toDelete, err := vh.popUnackedRecords(connID, channel, deliveryTag, multiple, uint16(amqp.BASIC_ACK))
	if err != nil {
		return err
	}

	if len(toDelete) == 0 {
		return nil
	}

	connName := connID.String()
	for _, rec := range toDelete {
		vh.collector.RecordQueueAck(rec.QueueName)
		vh.collector.RecordChannelAck(connName, vh.Name, channel)
		if rec.Persistent && vh.persist != nil {
			_ = vh.persist.DeleteMessage(vh.Name, rec.QueueName, rec.Message.ID)
		}
	}

	return nil
}

func (vh *VHost) popUnackedRecords(connID ConnectionID, channel uint16, deliveryTag uint64, multiple bool, methodID uint16) ([]*DeliveryRecord, error) {
	key := ConnectionChannelKey{connID, channel}

	vh.mu.Lock()
	ch := vh.ChannelDeliveries[key]
	vh.mu.Unlock()
	if ch == nil {
		// multiple=true with tag 0 means "ack everything outstanding"; on a
		// channel that never had a delivery there is nothing to do.
		if multiple && deliveryTag == 0 {
			return nil, nil
		}
		return nil, unknownDeliveryTagError(deliveryTag, methodID)
	}

	removed := make([]*DeliveryRecord, 0)

	ch.mu.Lock()
	if multiple {
		// multiple=true with delivery-tag 0 means "all outstanding messages".
		// Any other tag must itself be outstanding, otherwise the client is
		// acknowledging a delivery it never received (or already acked).
		if deliveryTag != 0 {
			if _, exists := ch.UnackedByTag[deliveryTag]; !exists {
				ch.mu.Unlock()
				return nil, unknownDeliveryTagError(deliveryTag, methodID)
			}
		}
		// Must scan the flat map for numeric ordering.
		for tag, record := range ch.UnackedByTag {
			log.Debug().Uint64("tag", tag).Msg("Checking unacked tag for multiple ack")
			if deliveryTag == 0 || tag <= deliveryTag {
				removed = append(removed, record)
				deleteUnackedDelivery(ch, tag, record.ConsumerTag)
				log.Debug().Uint64("tag", tag).Msg("Removed unacked tag for multiple ack")
			}
		}
	} else {
		// Single ack: O(1) lookup in flat map
		record, exists := ch.UnackedByTag[deliveryTag]
		if !exists {
			ch.mu.Unlock()
			return nil, unknownDeliveryTagError(deliveryTag, methodID)
		}
		removed = append(removed, record)
		deleteUnackedDelivery(ch, deliveryTag, record.ConsumerTag)
		log.Debug().Uint64("tag", deliveryTag).Msg("Removed unacked tag for single ack")
	}
	// Notify any waiters that unacked state has changed
	select {
	case ch.unackedChanged <- struct{}{}:
	default:
	}

	ch.mu.Unlock()
	return removed, nil
}

func deleteUnackedDelivery(ch *ChannelDeliveryState, tag uint64, consumerTag string) {
	delete(ch.UnackedByTag, tag)

	// Remove from consumer's nested map
	if consumerMap, exists := ch.UnackedByConsumer[consumerTag]; exists {
		delete(consumerMap, tag)
		if len(consumerMap) == 0 {
			delete(ch.UnackedByConsumer, consumerTag)
		}
	}
}

func (vh *VHost) HandleBasicReject(connID ConnectionID, channel uint16, deliveryTag uint64, requeue bool) error {
	return vh.handleBasicNack(connID, channel, deliveryTag, false, requeue, uint16(amqp.BASIC_REJECT))
}

func (vh *VHost) HandleBasicNack(connID ConnectionID, channel uint16, deliveryTag uint64, multiple, requeue bool) error {
	return vh.handleBasicNack(connID, channel, deliveryTag, multiple, requeue, uint16(amqp.BASIC_NACK))
}

func (vh *VHost) handleBasicNack(connID ConnectionID, channel uint16, deliveryTag uint64, multiple, requeue bool, methodID uint16) error {
	recordsToNack, err := vh.popUnackedRecords(connID, channel, deliveryTag, multiple, methodID)
	if err != nil {
		return err
	}
	if len(recordsToNack) == 0 {
		return nil
	}

	if requeue {
		for _, record := range recordsToNack {
			vh.mu.Lock()
			queue := vh.Queues[record.QueueName]
			vh.mu.Unlock()
			if queue != nil {
				vh.collector.RecordQueueNack(queue.Name, false) // requeue=true: message stays in system
				if vh.handleTTLExpiration(record.Message, queue) {
					continue
				}
				log.Debug().Msgf("Requeuing message with delivery tag %d on channel %d\n", record.DeliveryTag, channel)
				vh.markAsRedelivered(record.Message.ID)
				queue.Push(record.Message)
				vh.collector.RecordQueueRequeue(record.QueueName)
			}
		}
		return nil
	}
	// Dead-letter or discard the messages
	for _, record := range recordsToNack {
		// Check if DLX extension is enabled and queue has DLX configured
		// DLX properties comes directly from arguments: single source of truth
		vh.mu.Lock()
		queue, exists := vh.Queues[record.QueueName]
		vh.mu.Unlock()
		if !exists {
			continue
		}
		vh.collector.RecordQueueNack(queue.Name, true) // discard: message removed from system

		ok := vh.handleDeadLetter(queue, record.Message, REASON_REJECTED)
		if ok {
			continue
		}
		// Discard the message (no DLX configured or dead-lettering failed)
		log.Debug().Uint64("delivery_tag", record.DeliveryTag).Uint16("channel", channel).Msg("Nack: discarding message")
		if record.Persistent {
			_ = vh.persist.DeleteMessage(vh.Name, record.QueueName, record.Message.ID)
		}
	}
	return nil
}

// handleDeadLetter checks if dead-lettering is possible and performs it
func (vh *VHost) handleDeadLetter(queue *Queue, msg Message, reason ReasonType) bool {
	if vh.ActiveExtensions["dlx"] && vh.DeadLetterer != nil {
		vh.mu.Lock()
		args := queue.Props.Arguments
		vh.mu.Unlock()
		dlx, dlxExists := args["x-dead-letter-exchange"].(string)

		if dlxExists && dlx != "" {
			err := vh.DeadLetterer.DeadLetter(msg, queue, reason)
			if err == nil {
				log.Debug().Msg("Dead-lettering succeeded")
				// Dead-lettering succeeded
				return true
			}
			log.Error().Err(err).Msg("Dead-lettering failed")
		}
	}
	// No DLX configured or dead-lettering not possible
	return false
}

func (vh *VHost) HandleBasicQos(connID ConnectionID, channel uint16, prefetchCount uint16, global bool) error {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	key := ConnectionChannelKey{connID, channel}
	state := vh.ChannelDeliveries[key]
	// fetch the channel delivery state if global is true
	if state == nil {
		state = &ChannelDeliveryState{
			UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
			UnackedByTag:      make(map[uint64]*DeliveryRecord),
			unackedChanged:    make(chan struct{}, 1),
			FlowActive:        true, // Default to flow active
		}
		vh.ChannelDeliveries[key] = state
	}
	// The delivery loop reads these under state.mu only (shouldThrottle).
	// Lock order: vh.mu -> state.mu.
	state.mu.Lock()
	defer state.mu.Unlock()
	if global {
		state.GlobalPrefetchCount = prefetchCount
		state.PrefetchGlobal = true
	} else {
		// Store the prefetch count to apply to new consumers if global is false
		state.NextPrefetchCount = prefetchCount
	}
	return nil
}
