package vhost

import (
	"fmt"
	"testing"
	"time"
)

// deleteWithin deletes a queue and fails the test if that takes longer than
// timeout, which means it hung waiting for the delivery loop.
func deleteWithin(t *testing.T, vh *VHost, name string, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- vh.DeleteQueuebyName(name) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeleteQueuebyName(%s): %v", name, err)
		}
	case <-time.After(timeout):
		t.Fatalf("DeleteQueuebyName(%s) did not return within %s: it is waiting for the delivery loop", name, timeout)
	}
}

// A failed delivery cancels the consumer from inside the delivery loop. When
// that was the last consumer, the cancel stopped the loop and waited for it:
// the loop waited for itself, and deleting the queue then waited forever too
// (#214, path A).
func TestDeleteQueueAfterFailedDeliveryToLastConsumer(t *testing.T) {
	sender := newFailOnChannelSender(1)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "broken", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return len(vh.GetActiveConsumersForQueue("q")) == 0 }) {
		t.Fatal("the failed consumer was not removed")
	}

	deleteWithin(t, vh, "q", time.Second)
	if _, err := vh.CreateQueue("other", &QueueProperties{Arguments: map[string]any{}}, connID); err != nil {
		t.Fatalf("vhost unusable after the delete: %v", err)
	}
}

// After a failed delivery removes the last consumer, the queue keeps
// delivering to the next consumer (#214).
func TestQueueDeliversAgainAfterFailedDeliveryToLastConsumer(t *testing.T) {
	sender := newFailOnChannelSender(1)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "broken", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return len(vh.GetActiveConsumersForQueue("q")) == 0 }) {
		t.Fatal("the failed consumer was not removed")
	}

	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "q", "ok", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m2"})
	// m1 was requeued when its delivery failed, so both arrive.
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(2) == 2 }) {
		t.Fatalf("the next consumer got %d of 2 messages", sender.deliveredOn(2))
	}
}

// A consumer that stops reading its socket blocks the delivery loop in a
// write. Deleting the queue used to wait for that loop while holding vh.mu,
// hanging the whole vhost (#214).
func TestDeleteQueueWhileDeliveryIsBlocked(t *testing.T) {
	sender := newGateSender(true)
	vh := newLoopTestVHost(t, sender)
	t.Cleanup(func() { close(sender.gate) }) // let the blocked write finish
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "stuck", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	select {
	case <-sender.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}

	deleteWithin(t, vh, "q", time.Second)
	if _, err := vh.CreateQueue("other", &QueueProperties{Arguments: map[string]any{}}, connID); err != nil {
		t.Fatalf("vhost unusable after the delete: %v", err)
	}
}

// Deleting a queue while its loop delivers must never hang (#214).
func TestDeleteQueueDuringActiveDelivery(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("q%d", i)
		q, err := vh.CreateQueue(name, &QueueProperties{Arguments: map[string]any{}}, connID)
		if err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}
		if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, name, fmt.Sprintf("c%d", i), &ConsumerProperties{NoAck: true})); err != nil {
			t.Fatalf("RegisterConsumer: %v", err)
		}
		for j := 0; j < 20; j++ {
			q.Push(Message{ID: fmt.Sprintf("m%d-%d", i, j)})
		}
		deleteWithin(t, vh, name, 2*time.Second)
	}
}

// A delivery still in flight when its queue is deleted must not retry
// against a queue recreated under the same name: the old message belongs to
// the deleted queue.
func TestDeletedQueueLoopDoesNotDeliverToRecreatedQueue(t *testing.T) {
	sender := newFailOnChannelSender(1)
	sender.gate = make(chan struct{})
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	old, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "old", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	old.Push(Message{ID: "from-deleted-queue"})
	select {
	case <-sender.blocked: // the old loop is inside a delivery that will fail
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}

	deleteWithin(t, vh, "q", time.Second)
	replacement, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue (replacement): %v", err)
	}
	t.Cleanup(replacement.stopDeliveryLoop)
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "q", "new", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}

	close(sender.gate) // the old delivery fails now
	time.Sleep(200 * time.Millisecond)
	if n := sender.deliveredOn(2); n != 0 {
		t.Fatalf("the replacement queue's consumer received %d message(s) from the deleted queue", n)
	}
}

// The delivery loop removes a consumer whose delivery failed. If the queue
// was deleted and recreated (and the consumer key reused) before that
// removal takes vh.mu, it must leave the new queue and consumer alone.
func TestRemoveFailedConsumerIgnoresRecreatedQueue(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	old, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	oldConsumer := NewConsumer(connID, 1, "q", "c", &ConsumerProperties{NoAck: false})
	if _, err := vh.RegisterConsumer(oldConsumer); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	deleteWithin(t, vh, "q", time.Second)
	if err := vh.CancelConsumer(1, "c"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}

	replacement, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue (replacement): %v", err)
	}
	t.Cleanup(replacement.stopDeliveryLoop)
	newConsumer := NewConsumer(connID, 1, "q", "c", &ConsumerProperties{NoAck: false})
	if _, err := vh.RegisterConsumer(newConsumer); err != nil {
		t.Fatalf("RegisterConsumer (replacement): %v", err)
	}
	replacement.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the replacement consumer got nothing")
	}

	// The old loop's failed delivery is processed only now.
	if err := vh.removeFailedConsumer(old, oldConsumer); err != nil {
		t.Fatalf("removeFailedConsumer: %v", err)
	}
	if got := vh.GetActiveConsumersForQueue("q"); len(got) != 1 || got[0] != newConsumer {
		t.Fatalf("replacement queue consumers = %v, want only the new consumer", got)
	}
	if n := replacement.Len(); n != 0 {
		t.Fatalf("replacement queue holds %d messages: the new consumer's unacked message was requeued", n)
	}
}
