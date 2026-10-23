package vhost

import (
	"fmt"
	"testing"
	"time"
)

// A queue deleted while it still has a consumer, then declared again under
// the same name, belongs only to its new consumers: the old consumer neither
// receives its messages nor counts as one of its consumers (#312).
func TestDeletedQueueConsumersDoNotCarryOverToRecreatedQueue(t *testing.T) {
	const n = 100
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	props := func() *QueueProperties { return &QueueProperties{Arguments: map[string]any{}} }

	if _, err := vh.CreateQueue("q", props(), connID); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "old", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer old: %v", err)
	}
	deleteWithin(t, vh, "q", time.Second)

	q, err := vh.CreateQueue("q", props(), connID)
	if err != nil {
		t.Fatalf("CreateQueue again: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 2, "q", "new", &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer new: %v", err)
	}

	if got := vh.GetConsumerCountsAllQueues()["q"]; got != 1 {
		t.Errorf("recreated queue has %d consumers, want 1", got)
	}
	if got := q.consumers.Load(); got != 1 {
		t.Errorf("recreated queue's consumer count = %d, want 1", got)
	}

	for i := 0; i < n; i++ {
		q.Push(Message{ID: fmt.Sprintf("m%d", i)})
	}
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(2) == n }) {
		t.Errorf("new consumer got %d of %d messages", sender.deliveredOn(2), n)
	}
	if got := sender.deliveredOn(1); got != 0 {
		t.Errorf("the deleted queue's consumer got %d messages of the new queue", got)
	}
}

// A deleted queue's unacked deliveries stay ackable, but don't follow the
// consumer tag: a new consumer on the same channel and tag, on another queue,
// isn't held back by them under its prefetch, and recovering after it is
// cancelled doesn't move them into its queue (#312).
func TestDeletedQueueDeliveriesDoNotFollowReusedConsumerTag(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	props := func() *QueueProperties { return &QueueProperties{Arguments: map[string]any{}} }
	consumer := func(queue string) *Consumer {
		c := NewConsumer(connID, 1, queue, "c", &ConsumerProperties{NoAck: false})
		c.PrefetchCount = 1
		return c
	}

	a, err := vh.CreateQueue("a", props(), connID)
	if err != nil {
		t.Fatalf("CreateQueue a: %v", err)
	}
	if _, err := vh.RegisterConsumer(consumer("a")); err != nil {
		t.Fatalf("RegisterConsumer on a: %v", err)
	}
	a.Push(Message{ID: "from-a"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the message of a was not delivered")
	}
	deleteWithin(t, vh, "a", time.Second) // delivery tag 1 stays unacked

	b, err := vh.CreateQueue("b", props(), connID)
	if err != nil {
		t.Fatalf("CreateQueue b: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "b", time.Second) })
	if _, err := vh.RegisterConsumer(consumer("b")); err != nil {
		t.Fatalf("RegisterConsumer on b with the same tag: %v", err)
	}
	b.Push(Message{ID: "from-b"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 2 }) {
		t.Fatal("the new consumer (prefetch 1) got nothing: the deleted queue's delivery counts against it")
	}

	// Cancelling it keeps its delivery on the channel (#299), and the client
	// can still ack the deleted queue's delivery.
	if err := vh.CancelConsumer(1, "c"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	if err := vh.HandleBasicAck(connID, 1, 1, false); err != nil {
		t.Errorf("ack of the deleted queue's delivery: %v", err)
	}
	// basic.recover requeues the cancelled consumer's delivery into b.
	if err := vh.HandleBasicRecover(connID, 1, true); err != nil {
		t.Fatalf("HandleBasicRecover: %v", err)
	}
	if !waitFor(time.Second, func() bool { return b.Len() >= 1 }) {
		t.Fatal("the new consumer's unacked message was not requeued to b")
	}
	time.Sleep(50 * time.Millisecond)
	if n := b.Len(); n != 1 {
		t.Errorf("b holds %d messages after the recover, want 1", n)
	}

}

// A delivery in flight when its queue is deleted, whose send then fails, is
// dropped from both unacked indexes, leaving nothing behind on the channel.
func TestDeletedQueueFailedInFlightDeliveryLeavesNoUnacked(t *testing.T) {
	sender := newFailOnChannelSender(1)
	sender.gate = make(chan struct{})
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "c", &ConsumerProperties{NoAck: false})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	select {
	case <-sender.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("the delivery did not start")
	}
	deleteWithin(t, vh, "q", time.Second)
	close(sender.gate) // the send fails now

	state := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connID, 1})
	left := func() (byTag, byConsumer int) {
		state.mu.Lock()
		defer state.mu.Unlock()
		for _, m := range state.UnackedByConsumer {
			byConsumer += len(m)
		}
		return len(state.UnackedByTag), byConsumer
	}
	if !waitFor(2*time.Second, func() bool { byTag, _ := left(); return byTag == 0 }) {
		t.Fatal("the failed delivery is still tracked by delivery tag")
	}
	if _, byConsumer := left(); byConsumer != 0 {
		t.Errorf("%d failed deliveries are still tracked by consumer", byConsumer)
	}
}
