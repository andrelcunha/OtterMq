package vhost

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/rs/zerolog/log"
)

const (
	// MANAGEMENT_CONNECTION_ID is used to identify management API operations
	// that interact with queues/exchanges without a real consumer/connection.
	MANAGEMENT_CONNECTION_ID = "__management__"
)

type ConnectionID string

func (id ConnectionID) String() string {
	return string(id)
}

type ConsumerKey struct {
	Channel uint16
	Tag     string
}

// ConnectionChannelKey uniquely identifies a channel within a connection
type ConnectionChannelKey struct {
	ConnectionID ConnectionID
	Channel      uint16
}

type Consumer struct {
	Tag           string
	Channel       uint16
	QueueName     string
	ConnectionID  ConnectionID
	Active        atomic.Bool
	PrefetchCount uint16 // 0 means unlimited
	FrameMax      uint32 // negotiated frame-max of the consumer's connection; 0 means unlimited
	Props         *ConsumerProperties

	frameSender FrameSender
}

type ConsumerProperties struct {
	NoAck     bool           `json:"no_ack"`
	Exclusive bool           `json:"exclusive"`
	Arguments map[string]any `json:"arguments"`
}

func NewConsumer(connID ConnectionID, channel uint16, queueName, consumerTag string, props *ConsumerProperties) *Consumer {
	return &Consumer{
		Tag:          consumerTag,
		Channel:      channel,
		QueueName:    queueName,
		ConnectionID: connID,
		Props:        props,
	}
}

func (vh *VHost) RegisterConsumer(consumer *Consumer) (string, error) {
	vh.mu.Lock()

	// Apply consumer prefetch if it was set via QoS(global = false)
	channelKey := ConnectionChannelKey{consumer.ConnectionID, consumer.Channel}
	if state, exists := vh.ChannelDeliveries[channelKey]; exists {
		if !state.PrefetchGlobal && state.NextPrefetchCount > 0 {
			consumer.PrefetchCount = state.NextPrefetchCount
		}
	}

	// Check if queue exists
	_, ok := vh.Queues[consumer.QueueName]
	if !ok {
		vh.mu.Unlock()
		return "", errors.NewChannelError(
			fmt.Sprintf("no queue '%s' in vhost '%s'", consumer.QueueName, vh.Name),
			uint16(amqp.NOT_FOUND),
			uint16(amqp.BASIC),
			uint16(amqp.BASIC_CONSUME),
		)
	}

	var key ConsumerKey
	if consumer.Tag == "" {
		const maxRetries = 1000
		var retries int
		for {
			consumer.Tag = generateConsumerTag()
			key = ConsumerKey{consumer.Channel, consumer.Tag}
			if _, exists := vh.Consumers[key]; !exists {
				break
			}
			retries++
			if retries >= maxRetries {
				vh.mu.Unlock()
				// return fmt.Errorf("failed to generate unique consumer tag after %d attempts", maxRetries)
				return "", errors.NewChannelError(
					"failed to generate unique consumer tag",
					uint16(amqp.INTERNAL_ERROR),
					uint16(amqp.BASIC),
					uint16(amqp.BASIC_CONSUME),
				)
			}
		}
	} else {
		key = ConsumerKey{consumer.Channel, consumer.Tag}
		// Check for duplicates
		if _, exists := vh.Consumers[key]; exists {
			vh.mu.Unlock()
			return consumer.Tag, errors.NewChannelError(
				fmt.Sprintf("consumer tag '%s' already exists on channel %d", key.Tag, key.Channel),
				uint16(amqp.PRECONDITION_FAILED),
				uint16(amqp.BASIC),
				uint16(amqp.BASIC_CONSUME),
			)
		}
	}

	// Check if exclusive consumer already exists for this queue
	// or if trying to add exclusive when others exist
	if existingConsumers, exists := vh.ConsumersByQueue[consumer.QueueName]; exists {
		for _, c := range existingConsumers {
			if c.Props.Exclusive {
				vh.mu.Unlock()
				return "", errors.NewChannelError(
					fmt.Sprintf("exclusive consumer already exists for queue %s", consumer.QueueName),
					uint16(amqp.PRECONDITION_FAILED),
					uint16(amqp.BASIC),
					uint16(amqp.BASIC_CONSUME),
				)
			}
		}
		if consumer.Props.Exclusive && len(existingConsumers) > 0 {
			vh.mu.Unlock()
			// TODO: return a channel exception error - 405
			// return fmt.Errorf("cannot add exclusive consumer when other consumers exist for queue %s", consumer.QueueName)
			return consumer.Tag, errors.NewChannelError(
				fmt.Sprintf("cannot obtain exclusive access to queue '%s' because it is already in use", consumer.QueueName),
				uint16(amqp.RESOURCE_LOCKED),
				uint16(amqp.BASIC),
				uint16(amqp.BASIC_CONSUME),
			)
		}
	}

	// Activate consumer and register
	consumer.Active.Store(true)
	vh.Consumers[key] = consumer

	// Index for delivery
	// verify if map entry exists
	if _, exists := vh.ConsumersByQueue[consumer.QueueName]; !exists {
		vh.ConsumersByQueue[consumer.QueueName] = []*Consumer{}
	}
	vh.ConsumersByQueue[consumer.QueueName] = append(
		vh.ConsumersByQueue[consumer.QueueName],
		consumer,
	)
	// Index for channel (connection-scoped)
	if _, exists := vh.ConsumersByChannel[channelKey]; !exists {
		vh.ConsumersByChannel[channelKey] = []*Consumer{}
	}
	vh.ConsumersByChannel[channelKey] = append(
		vh.ConsumersByChannel[channelKey],
		consumer,
	)
	vh.collector.RecordConsumerAdded(consumer.QueueName)
	queue := vh.Queues[consumer.QueueName]
	queue.consumers.Store(int32(len(vh.ConsumersByQueue[consumer.QueueName])))
	vh.mu.Unlock()

	// The loop is started once and then lives as long as the queue; if it is
	// already running (possibly idle, with no consumers), wake it up.
	queue.startDeliveryLoop(vh)
	queue.notifyConsumersChanged()

	return consumer.Tag, nil
}

func (vh *VHost) CancelConsumer(channel uint16, tag string) error {
	return vh.cancelConsumer(channel, tag, nil)
}

// removeFailedConsumer cancels a consumer whose delivery failed; it runs on
// q's delivery goroutine. It does nothing if, by the time it holds vh.mu, q
// was deleted (the name may now belong to a recreated queue) or the
// consumer's key was reused.
func (vh *VHost) removeFailedConsumer(q *Queue, consumer *Consumer) error {
	key := ConsumerKey{consumer.Channel, consumer.Tag}
	return vh.cancelConsumer(consumer.Channel, consumer.Tag, func() bool {
		return vh.Queues[q.Name] == q && vh.Consumers[key] == consumer
	})
}

// cancelConsumer removes a consumer. The queue's delivery loop keeps running
// (it idles without consumers). If guard is set, it is checked with vh.mu
// held and the cancel is skipped when it returns false. The consumer's
// unacked messages are requeued after vh.mu is released.
func (vh *VHost) cancelConsumer(channel uint16, tag string, guard func() bool) error {
	key := ConsumerKey{channel, tag}
	vh.mu.Lock()
	if guard != nil && !guard() {
		vh.mu.Unlock()
		return nil
	}
	consumer, exists := vh.Consumers[key]
	if !exists {
		vh.mu.Unlock()
		return errors.NewChannelError(
			fmt.Sprintf("no consumer '%s' on channel %d", tag, channel),
			uint16(amqp.NOT_FOUND),
			uint16(amqp.BASIC),
			uint16(amqp.BASIC_CANCEL),
		)
	}
	work := vh.cancelConsumerLocked(key, consumer)
	vh.mu.Unlock()

	vh.requeue(work)
	return nil
}

// requeueWork is a batch of messages to push back onto a queue once vh.mu
// is released.
type requeueWork struct {
	queue       *Queue
	records     []*DeliveryRecord
	redelivered bool // mark the messages so their next delivery sets redelivered
}

// requeue pushes back the collected messages. It must be called without
// vh.mu: Push can enforce x-max-length and dead-letter the evicted messages,
// which publishes and takes vh.mu.
func (vh *VHost) requeue(work []requeueWork) {
	for _, w := range work {
		for _, record := range w.records {
			if w.redelivered {
				vh.markAsRedelivered(record.Message.ID)
			}
			w.queue.Push(record.Message)
			vh.collector.RecordQueueRequeue(w.queue.Name)
		}
	}
}

// cancelConsumerLocked removes the consumer from every index, takes its
// unacked deliveries and deletes its queue if that was the last consumer of
// an auto-delete queue. The caller holds vh.mu and must pass the returned
// work to requeue after releasing it.
func (vh *VHost) cancelConsumerLocked(key ConsumerKey, consumer *Consumer) []requeueWork {
	consumer.Active.Store(false)
	delete(vh.Consumers, key)

	var work []requeueWork
	channelKey := ConnectionChannelKey{consumer.ConnectionID, consumer.Channel}
	if state := vh.ChannelDeliveries[channelKey]; state != nil {
		records := takeUnackedForConsumer(state, consumer.Tag)
		if len(records) > 0 {
			if queue, ok := vh.Queues[consumer.QueueName]; ok {
				log.Debug().
					Str("consumer", consumer.Tag).
					Str("queue", consumer.QueueName).
					Int("count", len(records)).
					Msg("Requeuing unacked messages for canceled consumer")
				work = append(work, requeueWork{queue: queue, records: records, redelivered: true})
			} else {
				log.Warn().
					Str("consumer", consumer.Tag).
					Str("queue", consumer.QueueName).
					Int("count", len(records)).
					Msg("Cannot requeue unacked messages: queue not found")
			}
		}
	}

	// Remove from ConsumersByQueue
	consumersForQueue := vh.ConsumersByQueue[consumer.QueueName]
	for i, c := range consumersForQueue {
		if c.Tag == key.Tag && c.Channel == key.Channel {
			vh.ConsumersByQueue[consumer.QueueName] = append(consumersForQueue[:i], consumersForQueue[i+1:]...)
			vh.collector.RecordConsumerRemoved(consumer.QueueName)
			break
		}
	}
	if queue, exists := vh.Queues[consumer.QueueName]; exists {
		queue.consumers.Store(int32(len(vh.ConsumersByQueue[consumer.QueueName])))
		queue.notifyConsumersChanged()
		if len(vh.ConsumersByQueue[consumer.QueueName]) == 0 {
			// verify if the queue can be auto-deleted
			if deleted, err := vh.checkAutoDeleteQueueUnlocked(queue.Name); err != nil {
				log.Printf("Failed to check auto-delete queue: %v", err)
			} else if deleted {
				log.Printf("Queue %s was auto-deleted", queue.Name)
			}
		}
	}

	// Remove from ConsumersByChannel (connection-scoped)
	consumersForChannel := vh.ConsumersByChannel[channelKey]
	for i, c := range consumersForChannel {
		if c.Tag == key.Tag {
			vh.ConsumersByChannel[channelKey] = append(consumersForChannel[:i], consumersForChannel[i+1:]...)
			break
		}
	}
	return work
}

// removeQueueConsumersLocked drops the consumers of a deleted queue from
// every index and returns them. They are keyed by queue name, so left in
// place they would be counted and delivered to by a queue later declared
// with the same name. Their unacked deliveries are handled by
// detachQueueDeliveriesLocked. The caller holds vh.mu.
func (vh *VHost) removeQueueConsumersLocked(queueName string) []*Consumer {
	removed := vh.ConsumersByQueue[queueName]
	for _, consumer := range removed {
		consumer.Active.Store(false)
		key := ConsumerKey{consumer.Channel, consumer.Tag}
		if vh.Consumers[key] == consumer {
			delete(vh.Consumers, key)
		}
		channelKey := ConnectionChannelKey{consumer.ConnectionID, consumer.Channel}
		consumersForChannel := vh.ConsumersByChannel[channelKey]
		for i, c := range consumersForChannel {
			if c == consumer {
				vh.ConsumersByChannel[channelKey] = append(consumersForChannel[:i], consumersForChannel[i+1:]...)
				break
			}
		}
		vh.collector.RecordConsumerRemoved(queueName)
	}
	delete(vh.ConsumersByQueue, queueName)
	return removed
}

// consumerCancels is a batch of server-sent basic.cancel for consumers the
// broker removed, to send once vh.mu is released (no frame writes under it).
type consumerCancels struct {
	sender    FrameSender
	framer    amqp.Framer
	consumers []*Consumer
}

// consumerCancelsLocked prepares the basic.cancel notifications for removed
// consumers, leaving out those of skipConn (a connection that is closing).
// The caller holds vh.mu and calls send after releasing it.
func (vh *VHost) consumerCancelsLocked(removed []*Consumer, skipConn ConnectionID) consumerCancels {
	cancels := consumerCancels{sender: vh.frameSender, framer: vh.framer}
	for _, c := range removed {
		if skipConn != "" && c.ConnectionID == skipConn {
			continue
		}
		cancels.consumers = append(cancels.consumers, c)
	}
	return cancels
}

// cancelNotifyWait bounds how long send waits for the basic.cancel writes.
const cancelNotifyWait = time.Second

// send writes basic.cancel to each consumer whose client advertised
// consumer_cancel_notify. Others are not told, as the capability requires.
// The writes go out on one goroutine per connection, and send waits for
// them, so they normally precede whatever the caller sends next (such as
// queue.delete-ok, or a consume-ok reusing the tag). It waits at most
// cancelNotifyWait: a client that stopped reading must not hold up the
// queue deletion or connection cleanup that removed its consumers, and
// frame writes have no deadline yet (#244). Such a client isn't reading
// what follows either.
func (cc consumerCancels) send() {
	if cc.sender == nil || cc.framer == nil {
		return
	}
	byConn := make(map[ConnectionID][]*Consumer)
	for _, c := range cc.consumers {
		if cc.sender.ConsumerCancelNotify(c.ConnectionID) {
			byConn[c.ConnectionID] = append(byConn[c.ConnectionID], c)
		}
	}
	if len(byConn) == 0 {
		return
	}
	var wg sync.WaitGroup
	for connID, consumers := range byConn {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, c := range consumers {
				frame := cc.framer.CreateBasicCancelFrame(c.Channel, c.Tag)
				if err := cc.sender.SendFrame(connID, c.Channel, frame); err != nil {
					log.Debug().Err(err).
						Str("consumer", c.Tag).
						Uint16("channel", c.Channel).
						Msg("Failed to send basic.cancel for a removed consumer")
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(cancelNotifyWait):
		log.Warn().Msg("basic.cancel not written within 1s: a client is not reading")
	}
}

// detachQueueDeliveriesLocked marks every unacked delivery of a deleted
// queue as such and drops it from UnackedByConsumer or UnackedByGet, keeping
// it only in UnackedByTag. The client can still ack it, but a consumer that reuses the
// tag doesn't inherit it (in its prefetch, or when it is canceled), and
// nothing requeues it into a queue that later takes the name. The caller
// holds vh.mu.
func (vh *VHost) detachQueueDeliveriesLocked(queueName string) {
	for _, state := range vh.ChannelDeliveries {
		state.mu.Lock()
		for _, record := range state.UnackedByTag {
			if record.QueueName != queueName || record.QueueDeleted {
				continue
			}
			record.QueueDeleted = true
			untrackDeliverySource(state, record)
		}
		state.mu.Unlock()
	}
}

// takeUnackedForConsumer removes a consumer's unacked deliveries from the
// channel state and returns them.
func takeUnackedForConsumer(state *ChannelDeliveryState, consumerTag string) []*DeliveryRecord {
	state.mu.Lock()
	defer state.mu.Unlock()

	consumerUnacked := state.UnackedByConsumer[consumerTag]
	if len(consumerUnacked) == 0 {
		return nil
	}
	delete(state.UnackedByConsumer, consumerTag)
	records := make([]*DeliveryRecord, 0, len(consumerUnacked))
	for deliveryTag, record := range consumerUnacked {
		delete(state.UnackedByTag, deliveryTag)
		records = append(records, record)
	}
	return records
}

// checkAutoDeleteQueueUnlocked checks if a queue is auto-delete and has no consumers, and deletes it if so.
func (vh *VHost) checkAutoDeleteQueueUnlocked(name string) (bool, error) {
	queue, exists := vh.Queues[name]
	if !exists {
		return false, fmt.Errorf("queue %s not found", name)
	}
	if !queue.Props.AutoDelete {
		return false, nil
	}

	if len(vh.ConsumersByQueue[name]) > 0 {
		return false, nil
	}

	// The queue has no consumers left, so none is removed or notified.
	_, err := vh.deleteQueuebyNameUnlocked(name)
	if err != nil {
		return false, err
	}
	return true, nil
}

// CleanupChannel cancels a closed channel's consumers and requeues every
// message still unacked on it. The requeue happens after vh.mu is released.
func (vh *VHost) CleanupChannel(connID ConnectionID, channel uint16) {
	vh.mu.Lock()
	channelKey := ConnectionChannelKey{connID, channel}
	work := vh.cancelChannelConsumersLocked(channelKey)

	if state := vh.ChannelDeliveries[channelKey]; state != nil {
		// What is left are deliveries without a consumer (basic.get).
		state.mu.Lock()
		unackedMessages := fetchUnackedMessages(state)
		state.mu.Unlock()

		byQueue := make(map[string]int) // queue name -> index in work
		for _, record := range unackedMessages {
			queue, exists := vh.Queues[record.QueueName]
			if !exists {
				continue
			}
			i, seen := byQueue[record.QueueName]
			if !seen {
				i = len(work)
				byQueue[record.QueueName] = i
				work = append(work, requeueWork{queue: queue})
			}
			work[i].records = append(work[i].records, record)
		}
		delete(vh.ChannelDeliveries, channelKey)
	}

	// The transaction state is reset after vh.mu is released: a commit holds
	// the transaction lock while it publishes, which takes vh.mu.
	txState := vh.ChannelTransactions[channelKey]
	delete(vh.ChannelTransactions, channelKey)
	vh.mu.Unlock()

	if txState != nil {
		txState.Lock()
		// Discard buffered publishes and acks (implicit rollback)
		txState.BufferedPublishes = []BufferedPublish{}
		txState.BufferedAcks = []BufferedAck{}
		txState.InTransaction = false
		txState.Unlock()
	}

	vh.requeue(work)
}

// fetchUnackedMessages returns the channel's unacked deliveries from
// consumers and basic.get, leaving out those of deleted queues (which are
// only in UnackedByTag). The caller holds state.mu.
func fetchUnackedMessages(state *ChannelDeliveryState) []*DeliveryRecord {
	totalUnacked := len(state.UnackedByGet)
	for _, consumeMap := range state.UnackedByConsumer {
		totalUnacked += len(consumeMap)
	}

	records := make([]*DeliveryRecord, 0, totalUnacked)
	for _, consumeMap := range state.UnackedByConsumer {
		for _, record := range consumeMap {
			records = append(records, record)
		}
	}
	for _, record := range state.UnackedByGet {
		records = append(records, record)
	}
	return records
}

// cancelChannelConsumersLocked cancels every consumer on a channel. The
// caller holds vh.mu and must pass the returned work to requeue after
// releasing it.
func (vh *VHost) cancelChannelConsumersLocked(channelKey ConnectionChannelKey) []requeueWork {
	consumersForChannel := vh.ConsumersByChannel[channelKey]
	consumersCopy := make([]*Consumer, len(consumersForChannel))
	copy(consumersCopy, consumersForChannel)

	var work []requeueWork
	for _, c := range consumersCopy {
		key := ConsumerKey{c.Channel, c.Tag}
		if vh.Consumers[key] != c {
			continue // already cancelled
		}
		work = append(work, vh.cancelConsumerLocked(key, c)...)
	}
	delete(vh.ConsumersByChannel, channelKey)
	return work
}

// CleanupConnection cancels a closed connection's consumers and deletes its
// exclusive queues. Unacked messages are requeued after vh.mu is released.
func (vh *VHost) CleanupConnection(connID ConnectionID) {
	vh.mu.Lock()

	// Cancel each consumer of this connection
	var work []requeueWork
	for key, consumer := range vh.Consumers {
		if consumer.ConnectionID == connID {
			work = append(work, vh.cancelConsumerLocked(key, consumer)...)
		}
	}

	// Clean up owned queues
	// it is expected that some (if not all) exclusive queues were already deleted
	// during consumer cancellation above. Consumers of these queues on other
	// connections, if any, are told; this connection is closing.
	var queuesToDelete []string
	for queueName, queue := range vh.Queues {
		if queue.OwnerConn == connID {
			if queue.Props.Exclusive {
				queuesToDelete = append(queuesToDelete, queueName)
			} else {
				queue.OwnerConn = ""
			}
		}
	}
	var removed []*Consumer
	for _, queueName := range queuesToDelete {
		queueConsumers, err := vh.deleteQueuebyNameUnlocked(queueName)
		removed = append(removed, queueConsumers...)
		if err != nil {
			log.Error().Str("queue", queueName).Msg("Error deleting exclusive queue")
		}
	}
	cancels := vh.consumerCancelsLocked(removed, connID)
	vh.mu.Unlock()

	cancels.send()
	vh.requeue(work)
}

func (vh *VHost) GetActiveConsumersForQueue(queueName string) []*Consumer {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	consumers := vh.ConsumersByQueue[queueName]
	if consumers == nil {
		return nil
	}
	// Filter only active consumers
	activeConsumers := make([]*Consumer, 0, len(consumers))
	for _, consumer := range consumers {
		if consumer.Active.Load() {
			activeConsumers = append(activeConsumers, consumer)
		}
	}
	return activeConsumers
}

// GetAllConsumers returns a slice of all consumers in the vhost.
func (vh *VHost) GetAllConsumers() []*Consumer {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	result := make([]*Consumer, 0, len(vh.Consumers))
	for _, consumer := range vh.Consumers {
		result = append(result, consumer)
	}
	return result
}

func generatePseudoRandomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func generateConsumerTag() string {
	return "amq.ctag-" + generatePseudoRandomString(12)
}
