package vhost

import (
	"testing"
	"time"
)

// The tag basic.get deliveries used to be tracked under (#321). A client may
// pick it for a consumer like any other tag.
const formerBasicGetTag = "__basic_get__"

func plainQueueProps() *QueueProperties {
	return &QueueProperties{Arguments: map[string]any{}}
}

// basicGet takes one message from queue as basic.get (manual ack) does on
// the given channel and returns its delivery tag.
func basicGet(t *testing.T, vh *VHost, connID ConnectionID, channel uint16, queue string) uint64 {
	t.Helper()
	msg := vh.GetMessage(queue, false)
	if msg == nil {
		t.Fatalf("basic.get on %s: queue empty", queue)
	}
	state := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connID, channel})
	return state.TrackDelivery(false, msg, queue)
}

func newManualAckConsumer(connID ConnectionID, channel uint16, queue, tag string, prefetch uint16) *Consumer {
	c := NewConsumer(connID, channel, queue, tag, &ConsumerProperties{NoAck: false})
	c.PrefetchCount = prefetch
	return c
}

// A consumer tagged like the old basic.get sentinel isn't held back under
// its prefetch by the channel's basic.get deliveries (#321).
func TestConsumerTaggedBasicGetPrefetchCountsOnlyItsDeliveries(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()

	g, err := vh.CreateQueue("g", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue g: %v", err)
	}
	g.Push(Message{ID: "got"})
	basicGet(t, vh, connID, 1, "g")

	q, err := vh.CreateQueue("q", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue q: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", formerBasicGetTag, 1)); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the consumer (prefetch 1) got nothing: the channel's basic.get delivery counts against it")
	}
}

// Cancelling a consumer tagged like the old sentinel requeues its own
// deliveries only; the channel's basic.get deliveries stay unacked (#321).
func TestCancelConsumerTaggedBasicGetRequeuesOnlyItsDeliveries(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()

	g, err := vh.CreateQueue("g", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue g: %v", err)
	}
	g.Push(Message{ID: "got"})
	getTag := basicGet(t, vh, connID, 1, "g")

	q, err := vh.CreateQueue("q", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue q: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", formerBasicGetTag, 0)); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the consumer got nothing")
	}

	if err := vh.CancelConsumer(1, formerBasicGetTag); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	if !waitFor(time.Second, func() bool { return q.Len() >= 1 }) {
		t.Fatal("the consumer's unacked message was not requeued to q")
	}
	time.Sleep(50 * time.Millisecond)
	if n := q.Len(); n != 1 {
		t.Errorf("q holds %d messages after the cancel, want 1: the basic.get delivery moved into it", n)
	}
	if n := g.Len(); n != 0 {
		t.Errorf("g holds %d messages after the cancel, want 0: the basic.get delivery was requeued", n)
	}
	if err := vh.HandleBasicAck(connID, 1, getTag, false); err != nil {
		t.Errorf("ack of the basic.get delivery after the cancel: %v", err)
	}
}

// basic.recover(requeue=false) redelivers to a consumer tagged like the old
// sentinel instead of requeuing its deliveries, and still requeues the
// channel's basic.get deliveries (#321).
func TestRecoverNoRequeueRedeliversToConsumerTaggedBasicGet(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()

	g, err := vh.CreateQueue("g", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue g: %v", err)
	}
	g.Push(Message{ID: "got"})
	basicGet(t, vh, connID, 1, "g")

	q, err := vh.CreateQueue("q", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue q: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", formerBasicGetTag, 0)); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the consumer got nothing")
	}
	// With the flow paused the delivery loop delivers nothing, so a second
	// delivery can only come from recover redelivering directly.
	if err := vh.HandleChannelFlow(connID, 1, false, false); err != nil {
		t.Fatalf("HandleChannelFlow: %v", err)
	}

	if err := vh.HandleBasicRecover(connID, 1, false); err != nil {
		t.Fatalf("HandleBasicRecover: %v", err)
	}
	if got := sender.deliveredOn(1); got != 2 {
		t.Errorf("deliveries on the channel = %d, want 2: the consumer's message was not redelivered to it", got)
	}
	if n := q.Len(); n != 0 {
		t.Errorf("q holds %d messages, want 0: the consumer's message was requeued", n)
	}
	if n := g.Len(); n != 1 {
		t.Errorf("g holds %d messages, want 1: the basic.get delivery was not requeued", n)
	}
}

// basic.recover, with or without requeue, and closing the channel requeue
// the channel's unacked basic.get deliveries.
func TestUnackedBasicGetDeliveriesAreRequeued(t *testing.T) {
	cases := []struct {
		name          string
		run           func(vh *VHost, connID ConnectionID) error
		markRedeliver bool
	}{
		{"recover requeue=true", func(vh *VHost, connID ConnectionID) error { return vh.HandleBasicRecover(connID, 1, true) }, true},
		{"recover requeue=false", func(vh *VHost, connID ConnectionID) error { return vh.HandleBasicRecover(connID, 1, false) }, true},
		// Closing the channel doesn't mark them redelivered today, for
		// basic.get deliveries only; that is outside #321.
		{"channel cleanup", func(vh *VHost, connID ConnectionID) error { vh.CleanupChannel(connID, 1); return nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vh := newLoopTestVHost(t, newGateSender(false))
			connID := newTestConsumerConnID()
			g, err := vh.CreateQueue("g", plainQueueProps(), connID)
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			g.Push(Message{ID: "a"})
			g.Push(Message{ID: "b"})
			basicGet(t, vh, connID, 1, "g")
			basicGet(t, vh, connID, 1, "g")

			if err := tc.run(vh, connID); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if n := g.Len(); n != 2 {
				t.Errorf("g holds %d messages, want 2", n)
			}
			if got := vh.GetUnackedMessageCountByChannel(connID, 1); got != 0 {
				t.Errorf("unacked on the channel = %d, want 0", got)
			}
			if tc.markRedeliver && (!vh.ShouldRedeliver("a") || !vh.ShouldRedeliver("b")) {
				t.Error("requeued basic.get messages are not marked redelivered")
			}
		})
	}
}

// basic.get deliveries are counted per channel and settled by ack, nack,
// reject and multiple acks alongside consumer deliveries.
func TestBasicGetDeliveriesAckNackRejectAndCounts(t *testing.T) {
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()

	g, err := vh.CreateQueue("g", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue g: %v", err)
	}
	q, err := vh.CreateQueue("q", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue q: %v", err)
	}
	t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", "c", 0)); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	for _, id := range []string{"g1", "g2", "g3", "g4"} {
		g.Push(Message{ID: id})
	}

	tag1 := basicGet(t, vh, connID, 1, "g")
	q.Push(Message{ID: "c1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("the consumer got nothing")
	}
	tag3 := basicGet(t, vh, connID, 1, "g")
	tag4 := basicGet(t, vh, connID, 1, "g")
	tag5 := basicGet(t, vh, connID, 1, "g")

	unacked := func() int { return vh.GetUnackedMessageCountByChannel(connID, 1) }
	if got := unacked(); got != 5 {
		t.Fatalf("unacked on the channel = %d, want 5", got)
	}
	if stats, ok := vh.GetChannelDeliveryStats(ConnectionChannelKey{connID, 1}); !ok || stats.UnackedCount != 5 {
		t.Fatalf("channel stats unacked = %d (ok=%v), want 5", stats.UnackedCount, ok)
	}
	if got := vh.GetUnackedMessageCountsAllQueues()["g"]; got != 4 {
		t.Errorf("unacked of g = %d, want 4", got)
	}

	// multiple=true settles the basic.get delivery and the consumer's.
	if err := vh.HandleBasicAck(connID, 1, tag1+1, true); err != nil {
		t.Fatalf("multiple ack: %v", err)
	}
	if got := unacked(); got != 3 {
		t.Fatalf("unacked after the multiple ack = %d, want 3", got)
	}
	if err := vh.HandleBasicNack(connID, 1, tag3, false, true); err != nil {
		t.Fatalf("nack: %v", err)
	}
	if n := g.Len(); n != 1 {
		t.Errorf("g holds %d messages after the nack, want 1", n)
	}
	if err := vh.HandleBasicReject(connID, 1, tag4, false); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := vh.HandleBasicAck(connID, 1, tag5, false); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := unacked(); got != 0 {
		t.Errorf("unacked at the end = %d, want 0", got)
	}
	if err := vh.HandleBasicAck(connID, 1, tag5, false); err == nil {
		t.Error("a second ack of the same basic.get delivery succeeded")
	}
}

// After a queue with an unacked delivery (from a consumer or basic.get) is
// deleted and declared again under the same name, neither basic.recover nor
// closing the channel puts the old delivery into the new queue or
// redelivers it (#312, #321).
func TestDeletedQueueDeliveriesAreNotRecoveredIntoRecreatedQueue(t *testing.T) {
	sources := []struct {
		name    string
		deliver func(t *testing.T, vh *VHost, sender *gateSender, connID ConnectionID, q *Queue)
		// tag a consumer registered after the delete reuses
		tag string
	}{
		{
			name: "consumer",
			deliver: func(t *testing.T, vh *VHost, sender *gateSender, connID ConnectionID, q *Queue) {
				if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", "c", 0)); err != nil {
					t.Fatalf("RegisterConsumer: %v", err)
				}
				q.Push(Message{ID: "old"})
				if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
					t.Fatal("the consumer got nothing")
				}
			},
			tag: "c",
		},
		{
			name: "basic.get",
			deliver: func(t *testing.T, vh *VHost, sender *gateSender, connID ConnectionID, q *Queue) {
				q.Push(Message{ID: "old"})
				basicGet(t, vh, connID, 1, "q")
			},
			tag: formerBasicGetTag,
		},
	}
	actions := []struct {
		name string
		run  func(t *testing.T, vh *VHost, connID ConnectionID, tag string)
	}{
		{"recover requeue=true", func(t *testing.T, vh *VHost, connID ConnectionID, _ string) {
			if err := vh.HandleBasicRecover(connID, 1, true); err != nil {
				t.Fatalf("HandleBasicRecover: %v", err)
			}
		}},
		{"recover requeue=false with the tag reused", func(t *testing.T, vh *VHost, connID ConnectionID, tag string) {
			if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "q", tag, 0)); err != nil {
				t.Fatalf("RegisterConsumer reusing %q: %v", tag, err)
			}
			if err := vh.HandleBasicRecover(connID, 1, false); err != nil {
				t.Fatalf("HandleBasicRecover: %v", err)
			}
		}},
		{"channel cleanup", func(t *testing.T, vh *VHost, connID ConnectionID, _ string) {
			vh.CleanupChannel(connID, 1)
		}},
	}
	for _, src := range sources {
		for _, act := range actions {
			t.Run(src.name+"/"+act.name, func(t *testing.T) {
				sender := newGateSender(false)
				vh := newLoopTestVHost(t, sender)
				connID := newTestConsumerConnID()
				q, err := vh.CreateQueue("q", plainQueueProps(), connID)
				if err != nil {
					t.Fatalf("CreateQueue: %v", err)
				}
				src.deliver(t, vh, sender, connID, q)
				deleteWithin(t, vh, "q", time.Second)

				q2, err := vh.CreateQueue("q", plainQueueProps(), connID)
				if err != nil {
					t.Fatalf("CreateQueue again: %v", err)
				}
				t.Cleanup(func() { deleteWithin(t, vh, "q", time.Second) })
				before := sender.deliveredOn(1)

				act.run(t, vh, connID, src.tag)
				time.Sleep(50 * time.Millisecond)

				if n := q2.Len(); n != 0 {
					t.Errorf("the recreated queue holds %d messages, want 0", n)
				}
				if got := sender.deliveredOn(1) - before; got != 0 {
					t.Errorf("%d deliveries after the delete, want 0", got)
				}
			})
		}
	}
}
