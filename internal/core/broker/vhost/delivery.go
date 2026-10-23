package vhost

import (
	"fmt"
	"sync"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/rs/zerolog/log"
)

type DeliveryRecord struct {
	DeliveryTag uint64
	ConsumerTag string
	QueueName   string
	Message     Message
	Persistent  bool
	// QueueDeleted is set, under the channel state's mu, once the queue is
	// deleted. Such a delivery can still be acked, but is never requeued or
	// dead-lettered: another queue may have taken the name.
	QueueDeleted bool
}

type ChannelDeliveryState struct {
	mu              sync.Mutex
	LastDeliveryTag uint64

	// Dual-index for unacked messages
	// Primary index: nested by consumer tag for O(1) lookups when acknowledging
	UnackedByConsumer map[string]map[uint64]*DeliveryRecord // consumerTag -> (deliveryTag -> DeliveryRecord)

	// Secondary index: flat map for O(1) deliveryTag lookups (ack/nack)
	UnackedByTag map[uint64]*DeliveryRecord // deliveryTag -> DeliveryRecord

	GlobalPrefetchCount   uint16
	NextPrefetchCount     uint16 // to be applied on the next consumer start
	PrefetchGlobal        bool
	unackedChanged        chan struct{}
	FlowActive            bool
	FlowInitiatedByBroker bool // default false
}

type FlowState struct {
	FlowActive            bool
	FlowInitiatedByBroker bool
}

// HandleChannelFlow processes a CHANNEL.FLOW request for the given channel
func (vh *VHost) HandleChannelFlow(connID ConnectionID, channel uint16, flowActive bool, flowInitiatedByBroker bool) error {
	channelKey := ConnectionChannelKey{connID, channel}
	channelState := vh.GetOrCreateChannelDelivery(channelKey)

	channelState.mu.Lock()
	defer channelState.mu.Unlock()
	channelState.FlowActive = flowActive
	channelState.FlowInitiatedByBroker = flowInitiatedByBroker
	log.Debug().
		Uint16("channel", channel).
		Bool("flow_active", flowActive).
		Bool("flow_initiated_by_broker", flowInitiatedByBroker).
		Msg("Channel flow state updated")
	return nil
}

func (vh *VHost) GetChannelFlowState(connID ConnectionID, channel uint16) FlowState {
	channelState := vh.getChannelDeliveryState(connID, channel)
	if channelState == nil {
		return FlowState{
			FlowActive:            true,
			FlowInitiatedByBroker: false,
		}
	}

	channelState.mu.Lock()
	defer channelState.mu.Unlock()

	return FlowState{
		FlowActive:            channelState.FlowActive,
		FlowInitiatedByBroker: channelState.FlowInitiatedByBroker,
	}
}

func (vh *VHost) deliverToConsumer(consumer *Consumer, msg Message, redelivered bool) error {
	if vh.frameSender == nil {
		return fmt.Errorf("frame sender not set in vhost")
	}
	// If caller didn't force redelivered, consult the mark set during requeue/recover.
	if !redelivered {
		redelivered = vh.ShouldRedeliver(msg.ID)
	}
	if !consumer.Active.Load() {
		return fmt.Errorf("consumer %s on channel %d is not active", consumer.Tag, consumer.Channel)
	}
	channelKey := ConnectionChannelKey{consumer.ConnectionID, consumer.Channel}
	ch := vh.GetOrCreateChannelDelivery(channelKey)

	ch.mu.Lock()
	// Checked again under ch.mu: deleting the queue clears Active and then
	// detaches the queue's deliveries under each ch.mu, so a delivery either
	// sees Active cleared here or is detached with the others.
	if !consumer.Active.Load() {
		ch.mu.Unlock()
		return fmt.Errorf("consumer %s on channel %d is not active", consumer.Tag, consumer.Channel)
	}
	ch.LastDeliveryTag++
	tag := ch.LastDeliveryTag

	track := !consumer.Props.NoAck // only track when manual ack is required
	if track {
		record := &DeliveryRecord{
			DeliveryTag: tag,
			ConsumerTag: consumer.Tag,
			QueueName:   consumer.QueueName,
			Message:     msg,
			Persistent:  msg.Properties.DeliveryMode == amqp.PERSISTENT,
		}

		// Dual-index: add to both maps
		ch.UnackedByTag[tag] = record

		if ch.UnackedByConsumer[consumer.Tag] == nil {
			ch.UnackedByConsumer[consumer.Tag] = make(map[uint64]*DeliveryRecord)
		}
		ch.UnackedByConsumer[consumer.Tag][tag] = record

		log.Debug().
			Uint64("delivery_tag", tag).
			Str("consumer", consumer.Tag).
			Str("queue", consumer.QueueName).
			Msg("Tracking delivery for manual ack")
	}

	ch.mu.Unlock()
	amqpMsg := msg.ToAMQPMessage()
	// Create frames and append them as a single slice
	frames := append(
		vh.framer.CreateBasicDeliverFrame(
			consumer.Channel,
			consumer.Tag,
			msg.Exchange,
			msg.RoutingKey,
			tag,
			redelivered,
		), append(
			vh.framer.CreateHeaderFrame(consumer.Channel, uint16(amqp.BASIC), amqpMsg),
			vh.framer.CreateBodyFrame(consumer.Channel, consumer.FrameMax, msg.Body)...)...)

	// if err := vh.framer.SendFrame(consumer.Connection, frames); err != nil {
	if err := vh.frameSender.SendFrame(consumer.ConnectionID, consumer.Channel, frames); err != nil {
		log.Error().Err(err).Msg("Failed to send frames")
		if track {
			ch.mu.Lock()
			deleteUnackedDelivery(ch, tag, consumer.Tag)

			ch.mu.Unlock()
		}
		return err
	}

	vh.collector.RecordQueueDelivery(consumer.QueueName, consumer.Props.NoAck)
	connName := consumer.ConnectionID.String()
	vh.collector.RecordChannelDeliver(connName, vh.Name, consumer.Channel, consumer.Props.NoAck)

	// Persistence
	if consumer.Props.NoAck && vh.persist != nil && msg.Properties.DeliveryMode == amqp.PERSISTENT {
		if err := vh.persist.DeleteMessage(vh.Name, consumer.QueueName, msg.ID); err != nil {
			log.Error().Err(err).Msg("Failed to delete persisted message after delivery with no-ack")
		}
	}

	if redelivered {
		// Clear the one-shot mark so subsequent deliveries default to non-redelivered.
		vh.clearRedeliveredMark(msg.ID)
	}
	return nil
}

func (vh *VHost) GetOrCreateChannelDelivery(channelKey ConnectionChannelKey) *ChannelDeliveryState {
	vh.mu.Lock()
	ch := vh.ChannelDeliveries[channelKey]
	if ch == nil {
		ch = &ChannelDeliveryState{
			UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
			UnackedByTag:      make(map[uint64]*DeliveryRecord),
			unackedChanged:    make(chan struct{}, 1),
			FlowActive:        true,
		}
		vh.ChannelDeliveries[channelKey] = ch
	}
	vh.mu.Unlock()
	return ch
}

func (vh *VHost) GetChannelDelivery(channelKey ConnectionChannelKey) *ChannelDeliveryState {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	ch := vh.ChannelDeliveries[channelKey]
	return ch
}

// ChannelDeliveryStats is a consistent copy of the delivery counters the
// management API reports for a channel.
type ChannelDeliveryStats struct {
	UnackedCount  int
	PrefetchCount uint16
	FlowActive    bool
}

// GetChannelDeliveryStats returns the channel's delivery counters, read under
// the channel's lock. ok is false if the channel has no delivery state yet.
func (vh *VHost) GetChannelDeliveryStats(channelKey ConnectionChannelKey) (stats ChannelDeliveryStats, ok bool) {
	state := vh.GetChannelDelivery(channelKey)
	if state == nil {
		return ChannelDeliveryStats{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return ChannelDeliveryStats{
		UnackedCount:  len(state.UnackedByTag),
		PrefetchCount: state.NextPrefetchCount,
		FlowActive:    state.FlowActive,
	}, true
}

func (vh *VHost) ShouldRedeliver(msgID string) bool {
	vh.redeliveredMu.Lock()
	_, exists := vh.redeliveredMessages[msgID]
	vh.redeliveredMu.Unlock()
	return exists
}

func (vh *VHost) clearRedeliveredMark(msgID string) {
	vh.redeliveredMu.Lock()
	delete(vh.redeliveredMessages, msgID)
	vh.redeliveredMu.Unlock()
}

// shouldThrottle checks if the consumer should be throttled based on the channel's delivery state
func (vh *VHost) shouldThrottle(consumer *Consumer, channelState *ChannelDeliveryState) bool {
	if channelState == nil {
		return false // No QoS set, no throttling
	}
	// It should throttle if:
	// a) Prefetch is global AND total unacked on channel >= global prefetch AND global prefetch > 0
	// b) Prefetch is per-consumer AND total unacked on channel for this consumer >= consumer prefetch AND consumer prefetch > 0
	// c) Flow is paused on the channel

	// One snapshot: the QoS and flow fields are written under channelState.mu.
	channelState.mu.Lock()
	flowActive := channelState.FlowActive
	prefetchGlobal := channelState.PrefetchGlobal
	globalPrefetchCount := channelState.GlobalPrefetchCount
	unackedChannel := len(channelState.UnackedByTag)
	unackedConsumer := len(channelState.UnackedByConsumer[consumer.Tag])
	channelState.mu.Unlock()

	reason := []struct {
		name      string
		condition bool
	}{
		{"flow paused", !flowActive},
		{"global qos", prefetchGlobal &&
			globalPrefetchCount > 0 &&
			unackedChannel >= int(globalPrefetchCount)},
		{"consumer_qos", !prefetchGlobal &&
			consumer.PrefetchCount > 0 &&
			unackedConsumer >= int(consumer.PrefetchCount)},
	}
	for _, r := range reason {
		if r.condition {
			log.Trace().Str("reason", r.name).
				Str("consumer", consumer.Tag).
				Uint16("channel", consumer.Channel).
				Msg("Throttling delivery")
			return true
		}
	}
	return false
}

func (vh *VHost) getChannelDeliveryState(connectionID ConnectionID, channel uint16) *ChannelDeliveryState {
	vh.mu.Lock()
	defer vh.mu.Unlock()

	key := ConnectionChannelKey{connectionID, channel}
	return vh.ChannelDeliveries[key]
}

func (ch *ChannelDeliveryState) TrackDelivery(noAck bool, msg *Message, queue string) uint64 {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.LastDeliveryTag++
	deliveryTag := ch.LastDeliveryTag
	consumerTag := BASIC_GET_SENTINEL // Basic.Get uses sentinel value

	// Track the delivery if manual ack is required
	if !noAck {
		record := &DeliveryRecord{
			DeliveryTag: deliveryTag,
			ConsumerTag: consumerTag,
			Message:     *msg,
			QueueName:   queue,
			Persistent:  msg.Properties.DeliveryMode == amqp.PERSISTENT,
		}
		// Dual-index: add to both maps
		ch.UnackedByTag[deliveryTag] = record

		if ch.UnackedByConsumer[consumerTag] == nil {
			ch.UnackedByConsumer[consumerTag] = make(map[uint64]*DeliveryRecord)
		}
		ch.UnackedByConsumer[consumerTag][deliveryTag] = record

		log.Debug().
			Uint64("delivery_tag", deliveryTag).
			Str("consumer", consumerTag).
			Str("queue", queue).
			Msg("Tracking Basic.Get delivery for manual ack")
	}
	return deliveryTag
}
