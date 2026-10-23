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
