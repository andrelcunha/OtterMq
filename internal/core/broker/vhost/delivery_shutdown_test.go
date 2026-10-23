package vhost

import (
	"context"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/testutil"
)

// cancelOnSendFrameSender simulates the delivery loop being stopped (the
// queue is deleted) while it is sending a message: its context is cancelled
// mid-delivery.
type cancelOnSendFrameSender struct {
	cancel context.CancelFunc
}

func (s *cancelOnSendFrameSender) SendFrame(ConnectionID, uint16, []byte) error {
	s.cancel()
	return nil
}

// newQueueWithStoppedLoop returns a queue with one registered manual-ack
// consumer and its delivery loop stopped, so a test can drive one loop
// iteration itself with ctx as the loop's context.
func newQueueWithStoppedLoop(t *testing.T, ctx context.Context, sender FrameSender) (*VHost, *Queue, ConnectionID) {
	t.Helper()
	vh := setupTestVHost()
	vh.framer = &testutil.MockFramer{}
	vh.SetFrameSender(sender)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q-shutdown", &QueueProperties{}, connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, q.Name, "ctag", &ConsumerProperties{})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	// RegisterConsumer starts the loop in a goroutine; wait for it, then stop it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		q.deliveryMu.Lock()
		running := q.delivering
		q.deliveryMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery loop did not start")
		}
		time.Sleep(time.Millisecond)
	}
	q.stopDeliveryLoop()
	q.deliveryCtx = ctx
	return vh, q, connID
}

// A message delivered while the loop is being stopped must not be pushed back
// as well: it is unacked, and cancelling the consumer requeues it from there.
// Pushing it too delivered it twice to the next consumer.
func TestDeliveryLoopStoppedMidDeliveryDoesNotRequeueDeliveredMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vh, q, connID := newQueueWithStoppedLoop(t, ctx, &cancelOnSendFrameSender{cancel: cancel})

	q.Push(Message{ID: "m1", Body: []byte("x")})
	q.deliverFromFIFOQueue(vh, nil)

	if n := q.Len(); n != 0 {
		t.Errorf("queue holds %d messages after the delivery, want 0 (the delivered message was pushed back)", n)
	}
	state := vh.getChannelDeliveryState(connID, 1)
	if state == nil {
		t.Fatal("no channel delivery state: the message was not delivered")
	}
	state.mu.Lock()
	unacked := len(state.UnackedByTag)
	state.mu.Unlock()
	if unacked != 1 {
		t.Errorf("unacked deliveries: got %d, want 1", unacked)
	}
}

// A message the loop gives up on because it is stopping is still requeued.
func TestDeliveryLoopStoppingWithoutConsumersRequeuesMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	vh, q, _ := newQueueWithStoppedLoop(t, ctx, &mockFrameSender{})
	if err := vh.CancelConsumer(1, "ctag"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	q.deliveryCtx = ctx
	cancel()

	if handled := q.deliverMessage(vh, Message{ID: "m1", Body: []byte("x")}, nil); handled {
		t.Fatal("deliverMessage reported a message it could not deliver as handled")
	}
	if !requeueOnShutdown(q, Message{ID: "m1", Body: []byte("x")}, false) {
		t.Fatal("requeueOnShutdown did not report the stopped loop")
	}
	if n := q.Len(); n != 1 {
		t.Errorf("queue holds %d messages, want 1 (the message was lost)", n)
	}
}
