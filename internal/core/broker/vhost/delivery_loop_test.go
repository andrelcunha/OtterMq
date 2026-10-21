package vhost

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Cancelling the last consumer used to stop the queue's delivery loop, waiting
// for it outside vh.mu. A consumer registering in that window saw the old loop
// still "delivering" and started none, so it never received anything (#218).
// Here the old loop is held inside a delivery to make the window certain.
func TestConsumerRegisteredWhileLastIsCancelledReceivesMessages(t *testing.T) {
	sender := newGateSender(true)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)

	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "a", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer a: %v", err)
	}
	q.Push(Message{ID: "m1"})
	select {
	case <-sender.blocked: // the loop is now inside the delivery to "a"
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery did not start")
	}

	cancelled := make(chan error, 1)
	go func() { cancelled <- vh.CancelConsumer(1, "a") }()
	time.Sleep(50 * time.Millisecond) // let the cancel reach the loop stop, if any

	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "q", "b", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer b: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let a (re)start of the loop happen, if any

	close(sender.gate) // the old delivery completes
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("CancelConsumer a: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CancelConsumer a did not return")
	}

	q.Push(Message{ID: "m2"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(2) > 0 }) {
		t.Fatal("consumer b is registered but received nothing: the queue has no delivery loop")
	}
}

// Cancelling one consumer while another registers, over and over: every
// iteration must deliver to the consumer that is left (#218).
func TestConsumerChurnNeverStallsQueue(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)

	tag := func(i int) string { return fmt.Sprintf("c%d", i) }
	channel := func(i int) uint16 { return uint16(i%2 + 1) }
	if _, err := vh.RegisterConsumer(NewConsumer(connID, channel(0), "q", tag(0), &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}

	const iterations = 1000
	stalls := 0
	for i := 1; i <= iterations; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := vh.CancelConsumer(channel(i-1), tag(i-1)); err != nil {
				t.Errorf("CancelConsumer: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := vh.RegisterConsumer(NewConsumer(connID, channel(i), "q", tag(i), &ConsumerProperties{NoAck: true})); err != nil {
				t.Errorf("RegisterConsumer: %v", err)
			}
		}()
		wg.Wait()

		before := sender.deliveries.Load()
		q.Push(Message{ID: fmt.Sprintf("m%d", i)})
		if !waitFor(time.Second, func() bool { return sender.deliveries.Load() > before }) {
			stalls++
			break // the queue has no loop now; later iterations would stall too
		}
	}
	if stalls > 0 {
		t.Fatalf("queue stalled with a live consumer (after %d deliveries)", sender.deliveries.Load())
	}
}

// The delivery loop now outlives its consumers, so deleting a queue whose
// consumers are gone must not deadlock with the idle loop: deletion still
// waits for the loop, and the idle loop must not need vh.mu meanwhile.
func TestDeleteQueueWithIdleLoopDoesNotHang(t *testing.T) {
	vh := newLoopTestVHost(t, newGateSender(false))
	connID := newTestConsumerConnID()
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("q%d", i)
		if _, err := vh.CreateQueue(name, &QueueProperties{Arguments: map[string]any{}}, connID); err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}
		if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, name, "c", &ConsumerProperties{NoAck: true})); err != nil {
			t.Fatalf("RegisterConsumer: %v", err)
		}
		if err := vh.CancelConsumer(1, "c"); err != nil {
			t.Fatalf("CancelConsumer: %v", err)
		}
		deleted := make(chan error, 1)
		go func() { deleted <- vh.DeleteQueuebyName(name) }()
		select {
		case err := <-deleted:
			if err != nil {
				t.Fatalf("DeleteQueuebyName: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("deleting idle queue %s hung (iteration %d)", name, i)
		}
	}
}

// When the last consumer of a priority queue leaves while the loop drains
// it, the loop goes back to idling (instead of requeuing in a busy loop) and
// the next consumer gets the remaining messages.
func TestPriorityQueueDrainResumesForNextConsumer(t *testing.T) {
	sender := newGateSender(true)
	vh := newLoopTestVHost(t, sender)
	vh.maxPriority = 10
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("pq", &QueueProperties{Arguments: map[string]any{"x-max-priority": 10}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)
	if q.maxPriority == 0 {
		t.Fatal("queue is not a priority queue")
	}

	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "pq", "a", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer a: %v", err)
	}
	const total = 5
	for i := 0; i < total; i++ {
		q.Push(Message{ID: fmt.Sprintf("m%d", i)})
	}
	select {
	case <-sender.blocked: // the loop is delivering the first message to "a"
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery did not start")
	}
	var openGate sync.Once
	release := func() { openGate.Do(func() { close(sender.gate) }) }
	t.Cleanup(release)
	cancelled := make(chan error, 1)
	go func() { cancelled <- vh.CancelConsumer(1, "a") }()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("CancelConsumer a: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelConsumer a waited for the delivery in flight")
	}
	release()

	time.Sleep(100 * time.Millisecond) // the loop notices it has no consumers
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "pq", "b", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer b: %v", err)
	}
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(2) == total-1 }) {
		t.Fatalf("consumer b got %d of the %d remaining messages", sender.deliveredOn(2), total-1)
	}
}

// A delivery waiting because its only consumer is throttled (here: flow
// paused) must notice when that consumer is cancelled and another one
// subscribes. It used to keep retrying the cancelled consumer, for up to 100
// one-second rounds.
func TestThrottledDeliveryPicksUpReplacementConsumer(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)

	if err := vh.HandleChannelFlow(connID, 1, false, false); err != nil {
		t.Fatalf("HandleChannelFlow: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "paused", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	time.Sleep(100 * time.Millisecond) // the loop is now waiting on the paused consumer

	if err := vh.CancelConsumer(1, "paused"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "q", "ready", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	if !waitFor(500*time.Millisecond, func() bool { return sender.deliveredOn(2) == 1 }) {
		t.Fatal("the replacement consumer did not get the waiting message promptly")
	}
}
