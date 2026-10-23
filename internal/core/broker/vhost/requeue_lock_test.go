package vhost

import (
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/testutil"
	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
)

// newMaxLengthDLXSetup returns a vhost with the DLX and max-length extensions,
// a fanout dead-letter exchange "dlx" bound to "dlq", and a queue "q" with
// x-max-length=1 dead-lettering to "dlx". A manual-ack consumer with prefetch
// 1 on channel 1 holds m1 unacked, the delivery loop holds m2 (throttled),
// and m3 fills q to its max length.
func newMaxLengthDLXSetup(t *testing.T) (*VHost, ConnectionID, *Queue, *Queue) {
	t.Helper()
	vh := NewVhost("qll-dlx", VHostOptions{
		Persistence:     &dummy.DummyPersistence{},
		QueueBufferSize: 100,
		EnableDLX:       true,
		EnableQLL:       true,
	})
	vh.SetMetricsCollector(metrics.NewMockCollector(nil))
	vh.framer = &testutil.MockFramer{}
	sender := newGateSender(false)
	vh.SetFrameSender(sender)

	connID := newTestConsumerConnID()
	if err := vh.CreateExchange("dlx", FANOUT, &ExchangeProperties{Arguments: map[string]any{}}); err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	dlq, err := vh.CreateQueue("dlq", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue dlq: %v", err)
	}
	if err := vh.BindQueue("dlx", "dlq", "", nil, connID); err != nil {
		t.Fatalf("BindQueue: %v", err)
	}
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{
		"x-max-length":           int32(1),
		"x-dead-letter-exchange": "dlx",
	}}, connID)
	if err != nil {
		t.Fatalf("CreateQueue q: %v", err)
	}
	t.Cleanup(q.cancelDeliveryLoop) // never wait: in the failing case the loop is deadlocked
	if err := vh.HandleBasicQos(connID, 1, 1, false); err != nil {
		t.Fatalf("HandleBasicQos: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "t", &ConsumerProperties{NoAck: false})); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}

	q.Push(Message{ID: "m1"})
	if !waitFor(2*time.Second, func() bool { return sender.deliveredOn(1) == 1 }) {
		t.Fatal("m1 was not delivered")
	}
	q.Push(Message{ID: "m2"})
	if !waitFor(2*time.Second, func() bool { return q.Len() == 0 }) {
		t.Fatal("the delivery loop did not take m2")
	}
	q.Push(Message{ID: "m3"})
	if q.Len() != 1 {
		t.Fatalf("q holds %d messages, want 1 (at its max length)", q.Len())
	}
	return vh, connID, q, dlq
}

// finishesWithin runs f and reports whether it returned within timeout.
func finishesWithin(f func(), timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// checkMaxLengthAfterRequeue checks that the requeue went through max-length
// enforcement without losing anything: a message was evicted to dlq, all
// three messages end up in q or dlq, and the requeued m1 is among them.
// Which queue each one ends in varies: the requeue and the delivery loop
// handing back m2 push concurrently, and the max-length check and the push
// are not atomic.
func checkMaxLengthAfterRequeue(t *testing.T, q, dlq *Queue) {
	t.Helper()
	if !waitFor(2*time.Second, func() bool { return dlq.Len() >= 1 }) {
		t.Fatal("no message was dead-lettered to dlq")
	}
	// Wait until the delivery loop handed m2 back and every eviction landed:
	// m1, m2 and m3 are then all in q or dlq.
	if !waitFor(3*time.Second, func() bool { return q.Len()+dlq.Len() == 3 }) {
		t.Fatalf("messages lost: q holds %d and dlq %d, want 3 in total", q.Len(), dlq.Len())
	}
	drain := func(queue *Queue) []string {
		var ids []string
		for msg := queue.Pop(); msg != nil; msg = queue.Pop() {
			ids = append(ids, msg.ID)
		}
		return ids
	}
	inQ, inDLQ := drain(q), drain(dlq)
	for _, id := range append(append([]string{}, inQ...), inDLQ...) {
		if id == "m1" {
			return
		}
	}
	t.Errorf("the requeued m1 was lost (q holds %v, dlq holds %v)", inQ, inDLQ)
}

// Requeuing a cancelled consumer's unacked messages pushed them while holding
// vh.mu. With x-max-length and a dead-letter exchange, the push evicts a
// message and dead-letters it, which takes vh.mu again: the cancel deadlocked
// on itself and the vhost stopped responding (#215).
func TestCancelConsumerWithMaxLengthAndDLX(t *testing.T) {
	vh, _, q, dlq := newMaxLengthDLXSetup(t)
	if !finishesWithin(func() { _ = vh.CancelConsumer(1, "t") }, 2*time.Second) {
		t.Fatal("CancelConsumer deadlocked")
	}
	checkMaxLengthAfterRequeue(t, q, dlq)
}

// The same requeue on channel.close (#215).
func TestCleanupChannelWithMaxLengthAndDLX(t *testing.T) {
	vh, connID, q, dlq := newMaxLengthDLXSetup(t)
	if !finishesWithin(func() { vh.CleanupChannel(connID, 1) }, 2*time.Second) {
		t.Fatal("CleanupChannel deadlocked")
	}
	checkMaxLengthAfterRequeue(t, q, dlq)
}

// The same requeue when the client disconnects (#215).
func TestCleanupConnectionWithMaxLengthAndDLX(t *testing.T) {
	vh, connID, q, dlq := newMaxLengthDLXSetup(t)
	if !finishesWithin(func() { vh.CleanupConnection(connID) }, 2*time.Second) {
		t.Fatal("CleanupConnection deadlocked")
	}
	checkMaxLengthAfterRequeue(t, q, dlq)
}
