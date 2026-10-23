package vhost

import (
	"fmt"
	"testing"
	"time"
)

// trackConsumerDelivery records an unacked consumer delivery on the channel.
func trackConsumerDelivery(state *ChannelDeliveryState, consumerTag, queue string, msg Message) uint64 {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.LastDeliveryTag++
	tag := state.LastDeliveryTag
	record := &DeliveryRecord{DeliveryTag: tag, ConsumerTag: consumerTag, QueueName: queue, Message: msg}
	state.UnackedByTag[tag] = record
	if state.UnackedByConsumer[consumerTag] == nil {
		state.UnackedByConsumer[consumerTag] = make(map[uint64]*DeliveryRecord)
	}
	state.UnackedByConsumer[consumerTag][tag] = record
	return tag
}

func popAll(q *Queue) []string {
	var ids []string
	for m := q.Pop(); m != nil; m = q.Pop() {
		ids = append(ids, m.ID)
	}
	return ids
}

// A closed channel's deliveries, from its consumers and from basic.get, go
// back in delivery-tag order and are marked redelivered (#219).
func TestCleanupChannel_RequeuesAllInTagOrderAsRedelivered(t *testing.T) {
	vh := createTestVHost()
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("cleanup-order", nil, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	for _, tag := range []string{"c1", "c2"} {
		if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, q.Name, tag, &ConsumerProperties{})); err != nil {
			t.Fatalf("RegisterConsumer %s: %v", tag, err)
		}
	}
	state := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connID, 1})
	var want []string
	for i := 0; i < 12; i++ {
		msg := Message{ID: fmt.Sprintf("m%02d", i)}
		switch i % 3 {
		case 0:
			trackConsumerDelivery(state, "c1", q.Name, msg)
		case 1:
			state.TrackDelivery(false, &msg, q.Name)
		default:
			trackConsumerDelivery(state, "c2", q.Name, msg)
		}
		want = append(want, msg.ID)
	}

	vh.CleanupChannel(connID, 1)

	if len(vh.GetActiveConsumersForQueue(q.Name)) != 0 {
		t.Errorf("consumers left on the queue")
	}
	if vh.GetChannelDelivery(ConnectionChannelKey{connID, 1}) != nil {
		t.Errorf("delivery state left behind")
	}
	for _, id := range want {
		if !vh.ShouldRedeliver(id) {
			t.Errorf("%s not marked redelivered", id)
		}
	}
	got := popAll(q)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("requeue order %v, want %v", got, want)
	}
}

// CleanupConnection runs the channel cleanup for every channel the
// connection has state on, and leaves other connections alone.
func TestCleanupConnection_CleansEveryChannel(t *testing.T) {
	vh := createTestVHost()
	connA := newTestConsumerConnID()
	connB := newTestConsumerConnID()
	q, err := vh.CreateQueue("cleanup-conn", nil, connB)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	// A: channel 1 has a basic.get delivery and a prefetch, channel 2 only
	// a transaction, channel 3 a consumer delivery from a gone consumer.
	st1 := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connA, 1})
	st1.TrackDelivery(false, &Message{ID: "get-1"}, q.Name)
	st1.NextPrefetchCount = 5
	vh.GetOrCreateTransactionState(2, connA).InTransaction = true
	st3 := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connA, 3})
	trackConsumerDelivery(st3, "gone", q.Name, Message{ID: "consumer-3"})
	// B: the same channel numbers.
	stB := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connB, 1})
	stB.TrackDelivery(false, &Message{ID: "get-b"}, q.Name)
	vh.GetOrCreateTransactionState(2, connB)

	vh.CleanupConnection(connA)

	vh.mu.Lock()
	for key := range vh.ChannelDeliveries {
		if key.ConnectionID == connA {
			t.Errorf("delivery state left for channel %d", key.Channel)
		}
	}
	for key := range vh.ChannelTransactions {
		if key.ConnectionID == connA {
			t.Errorf("transaction state left for channel %d", key.Channel)
		}
	}
	_, keptDeliveries := vh.ChannelDeliveries[ConnectionChannelKey{connB, 1}]
	_, keptTx := vh.ChannelTransactions[ConnectionChannelKey{connB, 2}]
	vh.mu.Unlock()
	if !keptDeliveries || !keptTx {
		t.Errorf("connection B's state removed: deliveries=%v tx=%v", keptDeliveries, keptTx)
	}

	got := popAll(q)
	if len(got) != 2 {
		t.Fatalf("requeued %v, want get-1 and consumer-3", got)
	}
	for _, id := range got {
		if !vh.ShouldRedeliver(id) {
			t.Errorf("%s not marked redelivered", id)
		}
	}
}

// Consumer keys are channel number + tag, not connection-scoped (#220).
// Cleaning up connection A must not cancel connection B's consumer with the
// same channel number and tag, even if A's channel index still points at an
// older consumer object under that key.
func TestCleanupChannel_KeepsOtherConnectionsConsumerWithSameKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cleanup func(vh *VHost, connA ConnectionID)
	}{
		{"channel", func(vh *VHost, connA ConnectionID) { vh.CleanupChannel(connA, 1) }},
		{"connection", func(vh *VHost, connA ConnectionID) { vh.CleanupConnection(connA) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vh := createTestVHost()
			connA := newTestConsumerConnID()
			connB := newTestConsumerConnID()
			consumerB := NewConsumer(connB, 1, "test-queue", "shared", &ConsumerProperties{})
			if _, err := vh.RegisterConsumer(consumerB); err != nil {
				t.Fatalf("RegisterConsumer: %v", err)
			}
			stale := NewConsumer(connA, 1, "test-queue", "shared", &ConsumerProperties{})
			stale.Active.Store(true)
			keyA := ConnectionChannelKey{connA, 1}
			vh.mu.Lock()
			vh.ConsumersByChannel[keyA] = []*Consumer{stale}
			vh.mu.Unlock()

			tc.cleanup(vh, connA)

			vh.mu.Lock()
			defer vh.mu.Unlock()
			if vh.Consumers[ConsumerKey{1, "shared"}] != consumerB {
				t.Errorf("connection B's consumer was removed from Consumers")
			}
			if qc := vh.ConsumersByQueue["test-queue"]; len(qc) != 1 || qc[0] != consumerB {
				t.Errorf("ConsumersByQueue = %v, want only B's consumer", qc)
			}
			if len(vh.ConsumersByChannel[ConnectionChannelKey{connB, 1}]) != 1 {
				t.Errorf("connection B's channel index changed")
			}
			if !consumerB.Active.Load() {
				t.Errorf("connection B's consumer deactivated")
			}
		})
	}
}

// ReleaseChannelDeliveries, which the broker runs when it sends
// channel.close, must not wait for the transaction lock: a failing tx.commit
// sends channel.close while holding it.
func TestReleaseChannelDeliveries_DoesNotTakeTransactionLock(t *testing.T) {
	vh := createTestVHost()
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("release-tx", nil, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	st := vh.GetOrCreateChannelDelivery(ConnectionChannelKey{connID, 1})
	st.TrackDelivery(false, &Message{ID: "m"}, q.Name)
	txState := vh.GetOrCreateTransactionState(1, connID)
	txState.Lock()
	txState.InTransaction = true

	if !finishesWithin(func() { vh.ReleaseChannelDeliveries(connID, 1) }, 2*time.Second) {
		txState.Unlock()
		t.Fatal("ReleaseChannelDeliveries blocked on the transaction lock")
	}
	txState.Unlock()
	if q.Len() != 1 {
		t.Errorf("queue has %d messages, want 1 requeued", q.Len())
	}
	if vh.GetTransactionState(1, connID) != txState {
		t.Errorf("transaction state dropped before close-ok")
	}

	vh.CleanupChannel(connID, 1)
	if vh.GetTransactionState(1, connID) != nil {
		t.Errorf("transaction state left after CleanupChannel")
	}
	txState.Lock()
	inTx := txState.InTransaction
	txState.Unlock()
	if inTx {
		t.Errorf("transaction not rolled back")
	}
}
