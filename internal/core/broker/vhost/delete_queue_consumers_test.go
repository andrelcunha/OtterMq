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
// isn't held back by them under its prefetch, and canceling it doesn't move
// them into its queue (#312).
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

	if err := vh.CancelConsumer(1, "c"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	if !waitFor(time.Second, func() bool { return b.Len() >= 1 }) {
		t.Fatal("the new consumer's unacked message was not requeued to b")
	}
	time.Sleep(50 * time.Millisecond)
	if n := b.Len(); n != 1 {
		t.Errorf("b holds %d messages after the cancel, want 1: the deleted queue's message moved into it", n)
	}

	// The client can still ack the deleted queue's delivery.
	if err := vh.HandleBasicAck(connID, 1, 1, false); err != nil {
		t.Errorf("ack of the deleted queue's delivery: %v", err)
	}
}
