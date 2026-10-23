package vhost

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/ottermq/ottermq/pkg/persistence"
	"github.com/rs/zerolog/log"
)

const (
	MIN_BUFFER_SIZE = 100
)

type QueueArgs map[string]any

type Queue struct {
	Name  string           `json:"name"`
	Props *QueueProperties `json:"properties"`

	// FIFO queue (non-priority)
	messages chan Message `json:"-"`

	// Priority queue (when x-max-priority > 0)
	priorityMessages map[uint8]chan Message `json:"-"`
	maxPriority      uint8                  `json:"-"`
	messageSignal    chan struct{}          `json:"-"` // Avoid blocking on empty priority queue

	count     int          `json:"-"`
	stopped   bool         `json:"-"` // true after queue is deleted; guarded by mu
	mu        sync.Mutex   `json:"-"`
	OwnerConn ConnectionID `json:"-"`

	vh *VHost `json:"-"` // Reference to parent VHost
	/* Delivery */
	deliveryCtx    context.Context    `json:"-"`
	deliveryCancel context.CancelFunc `json:"-"`
	delivering     bool
	deliveryWg     sync.WaitGroup `json:"-"` // Tracks delivery loop completion
	deliveryMu     sync.Mutex     `json:"-"` // Protects delivering and consumersChanged
	// consumersChanged wakes the delivery loop when consumers are added or
	// removed. Created lazily; use consumersSignal.
	consumersChanged chan struct{}
	// consumers mirrors len(vh.ConsumersByQueue[Name]); it is stored under
	// vh.mu and read by the delivery loop without it.
	consumers atomic.Int32

	/* Extension */
	maxLength uint32 `json:"-"`
}

type QueueProperties struct {
	Passive    bool      `json:"passive"`
	Durable    bool      `json:"durable"`
	AutoDelete bool      `json:"auto_delete"`
	Exclusive  bool      `json:"exclusive"`
	Arguments  QueueArgs `json:"arguments"`
}

func NewQueue(name string, bufferSize int, vh *VHost) *Queue {

	return &Queue{
		Name:             name,
		Props:            &QueueProperties{},
		messages:         make(chan Message, bufferSize), // Default FIFO
		priorityMessages: nil,                            // lazily init
		maxPriority:      0,
		messageSignal:    nil,
		count:            0,
		delivering:       false,
		maxLength:        0,
		vh:               vh,
	}
}

func (q *Queue) IsPersistenceEnabled() bool {
	return q.Props != nil &&
		q.Props.Durable &&
		q.vh != nil &&
		q.vh.persist != nil
}

// consumersSignal returns the channel that notifyConsumersChanged signals,
// creating it on first use.
func (q *Queue) consumersSignal() chan struct{} {
	q.deliveryMu.Lock()
	defer q.deliveryMu.Unlock()
	if q.consumersChanged == nil {
		q.consumersChanged = make(chan struct{}, 1)
	}
	return q.consumersChanged
}

// notifyConsumersChanged wakes the delivery loop after a consumer of this
// queue was registered or cancelled. It never blocks.
func (q *Queue) notifyConsumersChanged() {
	select {
	case q.consumersSignal() <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// startDeliveryLoop starts the queue's delivery goroutine if it is not
// running yet. The goroutine lives until the queue is deleted: when the queue
// has no consumers it waits for notifyConsumersChanged instead of exiting, so
// consumers coming and going can never leave the queue without a loop.
func (q *Queue) startDeliveryLoop(vh *VHost) {
	consumersChanged := q.consumersSignal()
	q.deliveryMu.Lock()
	if q.delivering {
		q.deliveryMu.Unlock()
		return // already running
	}
	q.deliveryCtx, q.deliveryCancel = context.WithCancel(context.Background())
	q.delivering = true
	q.deliveryWg.Add(1)
	q.deliveryMu.Unlock()

	go func() {
		defer q.deliveryWg.Done()
		for {
			select {
			case <-q.deliveryCtx.Done():
				log.Debug().Str("queue", q.Name).Msg("Stopping delivery loop")
				return
			default:
			}
			// Lock-free on purpose: an idle loop never needs vh.mu.
			if q.consumers.Load() == 0 {
				select {
				case <-q.deliveryCtx.Done():
				case <-consumersChanged:
				}
				continue
			}
			if q.maxPriority > 0 {
				q.deliverFromPriorityQueue(vh, consumersChanged)
			} else {
				q.deliverFromFIFOQueue(vh, consumersChanged)
			}
		}
	}()
}

// deliverFromPriorityQueue delivers the queued messages, highest priority
// first. It returns early when consumersChanged fires, so the loop re-checks
// whether the queue still has consumers before taking a message.
func (q *Queue) deliverFromPriorityQueue(vh *VHost, consumersChanged <-chan struct{}) {
	select {
	case <-q.deliveryCtx.Done():
		log.Debug().Str("queue", q.Name).Msg("Stopping delivery loop")
		return
	case <-consumersChanged:
		return
	case <-q.messageSignal:
		// There is at least one message in the priority queue
		for q.Len() > 0 {
			if q.consumers.Load() == 0 {
				// The last consumer left mid-drain: go back to idling, and
				// re-arm the signal so the next consumer drains the rest.
				q.signalMessages()
				return
			}
			msg := q.Pop()
			if msg == nil {
				return
			}
			handled := q.deliverMessage(vh, *msg, consumersChanged)

			isDone := requeueOnShutdown(q, *msg, handled)
			if isDone {
				return
			}
		}
	}
}

// deliverFromFIFOQueue delivers one message. Like deliverFromPriorityQueue,
// it returns early when consumersChanged fires.
func (q *Queue) deliverFromFIFOQueue(vh *VHost, consumersChanged <-chan struct{}) {
	select {
	case <-q.deliveryCtx.Done():
		log.Debug().Str("queue", q.Name).Msg("Stopping delivery loop")
		return
	case <-consumersChanged:
		return
	case msg := <-q.messages:
		q.mu.Lock()
		q.count--
		q.mu.Unlock()
		handled := q.deliverMessage(vh, msg, consumersChanged)

		isDone := requeueOnShutdown(q, msg, handled)
		if isDone {
			return
		}
	}
}

// requeueOnShutdown reports whether the delivery loop should stop because its
// context is done. If deliverMessage left the message unhandled (it stopped
// for the shutdown before delivering or requeuing it), the message is
// requeued here. A handled message is never pushed again: it was delivered,
// requeued or dead-lettered already, and pushing it would duplicate it.
func requeueOnShutdown(q *Queue, msg Message, handled bool) bool {
	select {
	case <-q.deliveryCtx.Done():
		if !handled {
			log.Debug().Str("queue", q.Name).Msg("Delivery loop cancelled, requeuing message and stopping")
			q.Push(msg)
		}
		return true
	default:
	}
	return false
}

// deliverMessage delivers msg to one of the queue's consumers. It returns
// false only when it gave up because the delivery loop is stopping, without
// delivering or requeuing msg; the caller then requeues it. In every other
// case msg has been delivered, requeued or dead-lettered. While every
// consumer is throttled it also wakes on consumersChanged and refreshes its
// consumer list.
func (q *Queue) deliverMessage(vh *VHost, msg Message, consumersChanged <-chan struct{}) bool {
	// Verify if TTL is enabled
	if vh.handleTTLExpiration(msg, q) {
		return true
	}

	log.Debug().Str("queue", q.Name).Str("id", msg.ID).Msg("Delivering message to consumers")
	consumers := vh.GetActiveConsumersForQueue(q.Name)

	// The last consumer went away after the loop took the message: put it
	// back. The loop then idles until a consumer arrives.
	if len(consumers) == 0 {
		log.Debug().Str("queue", q.Name).Msg("No consumers available, requeuing message")
		// Check if context was cancelled before requeuing
		select {
		case <-q.deliveryCtx.Done():
			log.Debug().Str("queue", q.Name).Msg("Delivery loop stopped, not requeuing")
			return false
		default:
			q.Push(msg)
			vh.collector.RecordQueueRequeue(q.Name)
			return true
		}
	}

	delivered := false
	maxRounds := 100 // Prevent infinite loop
	rounds := 0

	for !delivered && rounds < maxRounds {
		if q.stopping() {
			return false // the queue was deleted: don't retry by its name
		}
		rounds++
		allThrottled := true

		// Try each consumer once per round
		for i := 0; i < len(consumers); i++ {
			consumer := consumers[i]
			state := vh.getChannelDeliveryState(consumer.ConnectionID, consumer.Channel)

			if vh.shouldThrottle(consumer, state) {
				continue // Try next consumer
			}

			// Found available consumer
			allThrottled = false
			if err := vh.deliverToConsumer(consumer, msg, false); err != nil {
				if q.stopping() {
					// Deleted while this delivery was in flight. Consumers looked
					// up by name now may belong to a queue recreated under it.
					return false
				}
				log.Error().Err(err).Str("consumer", consumer.Tag).Msg("Delivery failed, removing consumer")
				if cancelErr := vh.removeFailedConsumer(q, consumer); cancelErr != nil {
					log.Error().Err(cancelErr).Str("consumer", consumer.Tag).Msg("Error cancelling consumer")
				}
				// Refresh consumer list and retry
				consumers = vh.GetActiveConsumersForQueue(q.Name)
				if len(consumers) == 0 {
					// Check if context was cancelled before requeuing
					select {
					case <-q.deliveryCtx.Done():
						return false // Stopping: the caller requeues it
					default:
						q.Push(msg)
						vh.collector.RecordQueueRequeue(q.Name)
						delivered = true // Exit loop
					}
				}
				break // Retry with new consumer list
			}
			delivered = true
			break // Successfully delivered
		}

		if allThrottled && !delivered {
			// All consumers are throttled, wait for signal or timeout
			// Try to get a signal from any consumer's channel
			var anyState *ChannelDeliveryState
			for _, c := range consumers {
				if s := vh.getChannelDeliveryState(c.ConnectionID, c.Channel); s != nil {
					anyState = s
					break
				}
			}

			if anyState != nil {
				select {
				case <-anyState.unackedChanged:
					// A slot opened up, retry immediately
					continue
				case <-consumersChanged:
					// A consumer came or went: retry with the current list.
					consumers = vh.GetActiveConsumersForQueue(q.Name)
					if len(consumers) == 0 {
						q.Push(msg)
						vh.collector.RecordQueueRequeue(q.Name)
						return true // the loop idles until a consumer arrives
					}
					continue
				case <-time.After(1 * time.Second):
					// Timeout, will retry or give up based on maxRounds
					continue
				case <-q.deliveryCtx.Done():
					return false
				}
			} else {
				// No state available, just sleep briefly
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	// If we exhausted retries, requeue the message
	if !delivered {
		log.Warn().Str("queue", q.Name).Int("rounds", rounds).Msg("Could not deliver after max rounds, requeuing")
		q.Push(msg)
		vh.collector.RecordQueueRequeue(q.Name)
	}
	return true
}

// stopping reports whether the delivery loop has been told to stop.
func (q *Queue) stopping() bool {
	return q.deliveryCtx.Err() != nil
}

// cancelDeliveryLoop tells the delivery loop to stop and returns without
// waiting for it. It is safe to call with vh.mu held and from the loop itself.
func (q *Queue) cancelDeliveryLoop() {
	q.deliveryMu.Lock()
	defer q.deliveryMu.Unlock()
	if q.delivering {
		q.deliveryCancel()
	}
}

// stopDeliveryLoop stops the delivery loop and waits until it has exited.
// The loop takes vh.mu, so never call this with vh.mu held, and never from
// the delivery goroutine itself.
func (q *Queue) stopDeliveryLoop() {
	q.deliveryMu.Lock()
	if !q.delivering {
		q.deliveryMu.Unlock()
		return
	}
	q.deliveryCancel()
	q.deliveryMu.Unlock()

	// Wait for delivery loop to actually stop
	q.deliveryWg.Wait()

	q.deliveryMu.Lock()
	q.delivering = false
	q.deliveryMu.Unlock()
}

func (vh *VHost) CreateQueue(name string, props *QueueProperties, connID ConnectionID) (*Queue, error) {
	vh.mu.Lock()
	defer vh.mu.Unlock()

	if name == "" {
		maxRetries := 5
		ok := false
		for range maxRetries {
			name = generateRandomQueueName()
			if _, exists := vh.Queues[name]; !exists {
				log.Debug().Str("queue", name).Msg("Generated random queue name")
				ok = true
				break
			} else {
				log.Debug().Str("queue", name).Msg("Generated already existing queue name, retrying")
			}
		}
		if !ok {
			serverErr := errors.NewChannelError(
				fmt.Sprintf("fail to generate queue name on '%s'", vh.Name),
				uint16(amqp.INTERNAL_ERROR),
				uint16(amqp.QUEUE),
				uint16(amqp.QUEUE_DECLARE),
			)
			return nil, serverErr
		}
		log.Debug().Str("queue", name).Msg("Using generated queue name")
	} else {
		// Passive declaration: error if queue doesn't exist
		existing, err := vh.retrievePassiveQueue(props, name)
		if err != nil {
			return nil, err
		}
		if existing == nil && props != nil && !props.Passive {
			existing = vh.Queues[name]
		}

		// Queue already exists: validate compatibility
		// Idempotent if properties match
		if existing != nil {
			if existing.Props == nil || props == nil {
				return nil, fmt.Errorf("queue %s already exists with incompatible properties", name)
			}
			if existing.Props.Durable != props.Durable ||
				existing.Props.AutoDelete != props.AutoDelete ||
				existing.Props.Exclusive != props.Exclusive ||
				!equalArgs(existing.Props.Arguments, props.Arguments) {
				// Raise Precondition Failed error
				return nil, errors.NewChannelError(
					fmt.Sprintf("queue %s already exists with different properties", name),
					uint16(amqp.PRECONDITION_FAILED),
					uint16(amqp.QUEUE),
					uint16(amqp.QUEUE_DECLARE),
				)
			}
			log.Debug().Str("queue", name).Msg("Queue already exists with matching properties")
			return existing, nil
		}
	}

	// Create new queue
	if props == nil {
		props = NewQueueProperties()
	}
	// DLX properties comes directly from arguments: single source of truth
	queue := NewQueue(name, vh.queueBufferSize, vh)
	queue.Props = props

	/* check for x-max-priority argument*/
	if maxPrio, ok := parseMaxPriorityArgument(props.Arguments); ok {
		if maxPrio > vh.maxPriority { // Clamp to VHost max
			log.Warn().
				Str("queue", name).
				Uint8("requested", maxPrio).
				Uint8("vhost_max", vh.maxPriority).
				Msg("Requested x-max-priority exceeds VHost limit, clamping")
			maxPrio = vh.maxPriority
		}

		if maxPrio > 0 {
			log.Debug().
				Str("queue", name).
				Uint8("max_priority", maxPrio).
				Msg("Initializing priority queue")
			queue.maxPriority = maxPrio
			queue.priorityMessages = make(map[uint8]chan Message)
			queue.messageSignal = make(chan struct{}, 1)
			// Channels are lazily created on fist push to each priority level
			//  -- avoids unnecessary memory usage
		}

	}

	if props.Exclusive {
		queue.OwnerConn = connID
	}
	vh.Queues[name] = queue

	if props.Durable {
		if err := vh.persist.SaveQueueMetadata(vh.Name, name, props.ToPersistence()); err != nil {
			log.Error().Err(err).Str("queue", name).Msg("Failed to save queue metadata")
		}
	}

	setQueueMaxLength(props, queue, name)

	log.Debug().Str("queue", name).Msg("Created queue")
	return queue, nil
}

// setQueueMaxLength sets the maxLength field of the queue based on its properties
func setQueueMaxLength(props *QueueProperties, queue *Queue, name string) {
	if props.Arguments == nil {
		queue.maxLength = 0
		return
	}
	if maxLen, ok := parseMaxLengthArgument(props.Arguments); ok {
		queue.maxLength = maxLen
		log.Debug().Str("queue", name).Uint32("max_length", queue.maxLength).Msg("Set max length for queue")
	}
}

func NewQueueProperties() *QueueProperties {
	return &QueueProperties{
		Passive:    false,
		Durable:    false,
		AutoDelete: false,
		Exclusive:  false,
		Arguments:  make(map[string]any),
	}
}

// DeleteQueuebyName deletes a queue and removes its consumers. Each removed
// consumer whose client advertised consumer_cancel_notify is sent
// basic.cancel once vh.mu is released, before this returns unless the write
// takes over a second (see consumerCancels.send).
func (vh *VHost) DeleteQueuebyName(name string) error {
	vh.mu.Lock()
	removed, err := vh.deleteQueuebyNameUnlocked(name)
	cancels := vh.consumerCancelsLocked(removed, "")
	vh.mu.Unlock()

	cancels.send()
	return err
}

// deleteQueuebyNameUnlocked deletes a queue and returns the consumers it
// removed, also when it fails after removing them. The caller holds vh.mu
// and decides which of them to send basic.cancel (see consumerCancelsLocked).
func (vh *VHost) deleteQueuebyNameUnlocked(name string) ([]*Consumer, error) {
	queue, exists := vh.Queues[name]
	if !exists { // double check
		return nil, errors.NewChannelError(
			fmt.Sprintf("no queue '%s' in vhost '%s'", name, vh.Name),
			uint16(amqp.NOT_FOUND),
			uint16(amqp.QUEUE),
			uint16(amqp.QUEUE_DELETE),
		)
	}

	// Stop the delivery loop without waiting for it: vh.mu is held, and the
	// loop may need vh.mu, be the caller itself, or be blocked writing to a
	// consumer that stopped reading. A delivery still in flight finishes;
	// anything it pushes back is discarded because the queue is stopped. The
	// messages channel is left open for the loop and garbage-collected.
	queue.cancelDeliveryLoop()
	queue.mu.Lock()
	queue.stopped = true
	queue.mu.Unlock()
	// Remove every binding to this queue, whatever the exchange type
	for _, exchange := range vh.Exchanges {
		vh.removeQueueBindingsUnlocked(exchange, queue)
	}

	// Remove the queue from the VHost's queue map
	delete(vh.Queues, name)
	removed := vh.removeQueueConsumersLocked(name)
	vh.detachQueueDeliveriesLocked(name)
	vh.collector.RemoveQueue(queue.Name)
	log.Debug().Str("queue", name).Msg("Deleted queue")

	// Persistence cleanup
	if queue.Props.Durable {
		if err := vh.persist.DeleteQueueMetadata(vh.Name, name); err != nil {
			log.Error().Err(err).Str("queue", name).Msg("Failed to delete queue from persistence")
			return removed, err
		}
	}
	return removed, nil
}

func (qp *QueueProperties) ToPersistence() persistence.QueueProperties {
	return persistence.QueueProperties{
		Durable:    qp.Durable,
		AutoDelete: qp.AutoDelete,
		Exclusive:  qp.Exclusive,
		Arguments:  qp.Arguments,
	}
}

func (q *Queue) Push(msg Message) {
	if q.maxLength > 0 && q.vh != nil {
		q.vh.QueueLengthLimiter.EnforceMaxLength(q)
	}

	if q.maxPriority > 0 {
		q.pushPriority(msg)
	} else {
		q.push(msg)
	}
}

// signalMessages tells the delivery loop that the priority queue holds
// messages. It never blocks.
func (q *Queue) signalMessages() {
	select {
	case q.messageSignal <- struct{}{}:
	default:
		// signal already pending
	}
}

func (q *Queue) pushPriority(msg Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		log.Debug().Str("queue", q.Name).Str("id", msg.ID).Msg("Push discarded: queue is stopped")
		return
	}

	// Clamp priority to max
	priority := min(msg.Properties.Priority, q.maxPriority)

	// Lazy chan allocation
	ch, exists := q.priorityMessages[priority]
	if !exists {
		// divide the buffer size equally among priority levels
		bufferPerPriority := q.vh.queueBufferSize / int(q.maxPriority+1)
		if bufferPerPriority < MIN_BUFFER_SIZE {
			bufferPerPriority = MIN_BUFFER_SIZE
		}
		ch = make(chan Message, bufferPerPriority)
		q.priorityMessages[priority] = ch
	}

	select {
	case ch <- msg:
		q.count++
		log.Debug().
			Str("queue", q.Name).
			Str("id", msg.ID).
			Uint8("priority", priority).
			Msg("Pushed message to queue")

		// Notify delivery loop that a new message is available
		q.signalMessages()
	default:
		log.Debug().
			Str("queue", q.Name).
			Str("id", msg.ID).
			Uint8("priority", priority).
			Msg("Queue channel full, dropping message")
	}
}

func (q *Queue) push(msg Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		log.Debug().Str("queue", q.Name).Str("id", msg.ID).Msg("Push discarded: queue is stopped")
		return
	}
	select {
	case q.messages <- msg:
		q.count++
		log.Debug().
			Str("queue", q.Name).
			Str("id", msg.ID).
			Msg("Pushed message to queue")
	default:
		log.Debug().
			Str("queue", q.Name).
			Str("id", msg.ID).
			Msg("Queue channel full, dropping message")
	}
}

func (q *Queue) Pop() *Message {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.maxPriority > 0 {
		return q.popPriorityUnlocked()
	}
	// else pop from FIFO
	return q.popUnlocked()
}

func (q *Queue) popUnlocked() *Message {
	select {
	case msg := <-q.messages:
		q.count--
		log.Debug().Str("queue", q.Name).Str("id", msg.ID).Int("body_size", len(msg.Body)).Msg("Popped message from queue")
		return &msg
	default:
		log.Debug().Str("queue", q.Name).Msg("Queue is empty")
		return nil
	}
}

func (q *Queue) popPriorityUnlocked() *Message {
	for prio := q.maxPriority; ; prio-- {
		if ch, exists := q.priorityMessages[prio]; exists {
			select {
			case msg := <-ch:
				q.count--
				log.Debug().
					Str("queue", q.Name).
					Str("id", msg.ID).
					Uint8("priority", prio).
					Str("body", string(msg.Body)).
					Msg("Popped message from priority queue")
				return &msg
			default:

			}
		}
		if prio == 0 {
			break
		}
	}
	log.Debug().Str("queue", q.Name).Msg("Priority queue is empty")
	return nil
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

func (vh *VHost) PurgeQueue(name string, connID ConnectionID) (uint32, error) {
	vh.mu.Lock()
	queue, exists := vh.Queues[name]
	vh.mu.Unlock()
	if !exists {
		text := amqp.NOT_FOUND.Format(fmt.Sprintf("no queue '%s' in vhost '%s'", name, vh.Name))
		return 0, errors.NewChannelError(text, uint16(amqp.NOT_FOUND), uint16(amqp.QUEUE), uint16(amqp.QUEUE_PURGE))
	}

	if err := vh.validateQueueOwnership(queue, connID, uint16(amqp.QUEUE), uint16(amqp.QUEUE_PURGE)); err != nil {
		return 0, err
	}
	purged := queue.StreamPurge(func(msg *Message) {
		if msg != nil {
			// Handle message expiration if needed
			if vh.handleTTLExpiration(*msg, queue) {
				// Message already expired and deleted
				return
			}
			if msg.Properties.DeliveryMode == amqp.PERSISTENT {
				if vh.persist != nil {
					if err := vh.persist.DeleteMessage(vh.Name, name, msg.ID); err != nil {
						log.Error().Err(err).Str("queue", name).Str("msg_id", msg.ID).Msg("Failed to delete persisted message during purge")
					}
				}
			}
		}
	})
	log.Debug().Str("queue", name).Uint32("purged_count", purged).Msg("Purged messages from queue")
	return purged, nil
}

func (q *Queue) StreamPurge(process func(*Message)) uint32 {
	q.mu.Lock()
	defer q.mu.Unlock()

	var purged uint32 = 0
	if q.maxPriority > 0 {
		// Purge priority queues
		for prio := q.maxPriority; ; prio-- {
			if ch, exists := q.priorityMessages[prio]; exists {
				for {
					select {
					case msg := <-ch:
						q.count--
						process(&msg)
						purged++
					default:
						// Channel empty
						goto nextPriority
					}
				}
			}
		nextPriority:
			if prio == 0 {
				break
			}
		}
	} else {
		// Purge FIFO queue
		for {
			msg := q.popUnlocked()
			if msg == nil {
				break
			}
			process(msg)
			purged++
		}
	}
	return purged
}

// retrievePassiveQueue checks if a passive queue declaration is requested and retrieves the queue if it exists.
func (vh *VHost) retrievePassiveQueue(props *QueueProperties, name string) (*Queue, error) {
	if props != nil && props.Passive {
		queue := vh.Queues[name]
		if queue == nil {
			text := amqp.NOT_FOUND.Format(fmt.Sprintf("no queue '%s' in vhost '%s'", name, vh.Name))
			return nil, errors.NewChannelError(text, uint16(amqp.NOT_FOUND), uint16(amqp.CHANNEL), uint16(amqp.QUEUE_DECLARE))
		} else {
			log.Debug().Str("queue", name).Msg("Passive queue declare: queue exists")
			return queue, nil
		}
	}
	return nil, nil
}

func generateRandomQueueName() string {
	return "amq.gen-" + generatePseudoRandomString(16)
}

// removeQueueBindingsUnlocked removes all of exchange's bindings to queue,
// under any routing key and with any arguments, and their persisted state.
// It works the same for every exchange type, since all of them store bindings
// by routing key. If the exchange is auto-delete and has no bindings left, it
// is deleted. Callers must hold vh.mu.
func (vh *VHost) removeQueueBindingsUnlocked(exchange *Exchange, queue *Queue) {
	removed := false
	for rk, bindings := range exchange.Bindings {
		kept := bindings[:0]
		for _, b := range bindings {
			if b.Queue.Name != queue.Name {
				kept = append(kept, b)
				continue
			}
			removed = true
			if exchange.Props.Durable && queue.Props.Durable && vh.persist != nil {
				if err := vh.persist.DeleteBindingState(vh.Name, exchange.Name, queue.Name, rk, b.Args); err != nil {
					log.Error().Err(err).Str("exchange", exchange.Name).Str("queue", queue.Name).Msg("Failed to delete binding state")
				}
			}
		}
		if len(kept) == 0 {
			delete(exchange.Bindings, rk)
		} else {
			exchange.Bindings[rk] = kept
		}
	}
	if !removed {
		return
	}
	if deleted, err := vh.checkAutoDeleteExchangeUnlocked(exchange.Name); err != nil {
		log.Error().Err(err).Str("exchange", exchange.Name).Msg("Failed to check auto-delete exchange")
	} else if deleted {
		log.Debug().Str("exchange", exchange.Name).Msg("Exchange was auto-deleted")
	}
}
