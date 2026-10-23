package vhost

import (
	"testing"
	"time"
)

// cancelledConsumerSetup registers a manual-ack consumer "c" on channel 1
// for queue a, has n messages delivered to it, and cancels it.
func cancelledConsumerSetup(t *testing.T, n int) (*VHost, *gateSender, ConnectionID, *Queue) {
	t.Helper()
	sender := newGateSender(false)
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()
	a, err := vh.CreateQueue("a", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue a: %v", err)
	}
	t.Cleanup(a.cancelDeliveryLoop)
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "a", "c", 0)); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	for i := 0; i < n; i++ {
		a.Push(Message{ID: "a" + string(rune('0'+i))})
	}
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == n }) {
		t.Fatalf("delivered %d, want %d", sender.deliveredOn(1), n)
	}
	if err := vh.CancelConsumer(1, "c"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	return vh, sender, connID, a
}

// After basic.cancel the deliveries are detached from the consumer tag but
// stay unacked on the channel, marked ConsumerCancelled (#299).
func TestCancelConsumer_DetachesDeliveries(t *testing.T) {
	vh, _, connID, a := cancelledConsumerSetup(t, 3)
	key := ConnectionChannelKey{connID, 1}
	state := vh.GetChannelDelivery(key)

	state.mu.Lock()
	byTag := len(state.UnackedByTag)
	_, byConsumer := state.UnackedByConsumer["c"]
	cancelled := 0
	for _, r := range state.UnackedByTag {
		if r.ConsumerCancelled && !r.QueueDeleted {
			cancelled++
		}
	}
	state.mu.Unlock()
	if byTag != 3 || cancelled != 3 {
		t.Errorf("UnackedByTag holds %d (%d marked cancelled), want 3 and 3", byTag, cancelled)
	}
	if byConsumer {
		t.Errorf("UnackedByConsumer still holds the cancelled tag")
	}
	if a.Len() != 0 {
		t.Errorf("the cancel requeued %d messages, want 0", a.Len())
	}
	// The management API's per-channel unacked count still counts them.
	if stats, ok := vh.GetChannelDeliveryStats(key); !ok || stats.UnackedCount != 3 {
		t.Errorf("GetChannelDeliveryStats = %+v, %v; want UnackedCount 3", stats, ok)
	}

	// They can be acked, nacked and rejected.
	if err := vh.HandleBasicAck(connID, 1, 1, false); err != nil {
		t.Errorf("ack after cancel: %v", err)
	}
	if err := vh.HandleBasicNack(connID, 1, 2, false, true); err != nil {
		t.Errorf("nack after cancel: %v", err)
	}
	if err := vh.HandleBasicReject(connID, 1, 3, false); err != nil {
		t.Errorf("reject after cancel: %v", err)
	}
	if a.Len() != 1 {
		t.Errorf("queue holds %d, want 1 (the nacked one)", a.Len())
	}
	if stats, _ := vh.GetChannelDeliveryStats(key); stats.UnackedCount != 0 {
		t.Errorf("UnackedCount %d after settling all, want 0", stats.UnackedCount)
	}
}

// A new consumer reusing the cancelled tag on the same channel isn't held
// back under its prefetch by the old deliveries.
func TestCancelConsumer_TagReuseDoesNotInheritPrefetch(t *testing.T) {
	vh, sender, connID, _ := cancelledConsumerSetup(t, 2)
	b, err := vh.CreateQueue("b", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue b: %v", err)
	}
	t.Cleanup(b.cancelDeliveryLoop)
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "b", "c", 2)); err != nil {
		t.Fatalf("RegisterConsumer reusing the tag: %v", err)
	}
	b.Push(Message{ID: "b0"})
	b.Push(Message{ID: "b1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 4 }) {
		t.Fatalf("the new consumer (prefetch 2) got %d, want 2: the old deliveries count against it", sender.deliveredOn(1)-2)
	}
}

// basic.recover(requeue=false) has no consumer to redeliver a cancelled
// consumer's deliveries to: they go back to their queue, not to a new
// consumer that reuses the tag on another queue.
func TestRecoverNoRequeue_RequeuesCancelledConsumerDeliveries(t *testing.T) {
	vh, sender, connID, a := cancelledConsumerSetup(t, 2)
	b, err := vh.CreateQueue("b", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue b: %v", err)
	}
	t.Cleanup(b.cancelDeliveryLoop)
	if _, err := vh.RegisterConsumer(newManualAckConsumer(connID, 1, "b", "c", 0)); err != nil {
		t.Fatalf("RegisterConsumer reusing the tag: %v", err)
	}

	if err := vh.HandleBasicRecover(connID, 1, false); err != nil {
		t.Fatalf("HandleBasicRecover: %v", err)
	}
	if a.Len() != 2 {
		t.Errorf("queue a holds %d, want 2 requeued", a.Len())
	}
	time.Sleep(50 * time.Millisecond)
	if n := sender.deliveredOn(1); n != 2 {
		t.Errorf("%d deliveries in total, want 2: a's messages went to b's consumer", n)
	}
	for _, id := range []string{"a0", "a1"} {
		if !vh.ShouldRedeliver(id) {
			t.Errorf("%s not marked redelivered", id)
		}
	}
}

// A cancelled consumer's delivery whose queue is then deleted is both
// ConsumerCancelled and QueueDeleted: it stays ackable but is never requeued,
// into a queue that later takes the name either.
func TestCancelledThenQueueDeleted_NeverRequeued(t *testing.T) {
	vh, _, connID, _ := cancelledConsumerSetup(t, 2)
	deleteWithin(t, vh, "a", time.Second)
	a2, err := vh.CreateQueue("a", plainQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue a again: %v", err)
	}
	t.Cleanup(a2.cancelDeliveryLoop)

	state := vh.GetChannelDelivery(ConnectionChannelKey{connID, 1})
	state.mu.Lock()
	for tag, r := range state.UnackedByTag {
		if !r.ConsumerCancelled || !r.QueueDeleted {
			t.Errorf("tag %d: cancelled=%v deleted=%v, want both", tag, r.ConsumerCancelled, r.QueueDeleted)
		}
	}
	state.mu.Unlock()

	if err := vh.HandleBasicAck(connID, 1, 1, false); err != nil {
		t.Errorf("ack: %v", err)
	}
	if err := vh.HandleBasicRecover(connID, 1, true); err != nil {
		t.Fatalf("HandleBasicRecover: %v", err)
	}
	vh.CleanupChannel(connID, 1)
	if a2.Len() != 0 {
		t.Errorf("the recreated queue holds %d, want 0", a2.Len())
	}
}

// Channel and connection close requeue a cancelled consumer's deliveries.
func TestCancelledConsumerDeliveries_RequeuedOnClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(vh *VHost, connID ConnectionID)
	}{
		{"channel", func(vh *VHost, connID ConnectionID) { vh.CleanupChannel(connID, 1) }},
		{"connection", func(vh *VHost, connID ConnectionID) { vh.CleanupConnection(connID) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vh, _, connID, a := cancelledConsumerSetup(t, 3)
			tc.close(vh, connID)
			if got := popAll(a); len(got) != 3 || got[0] != "a0" || got[2] != "a2" {
				t.Errorf("requeued %v, want [a0 a1 a2]", got)
			}
		})
	}
}
