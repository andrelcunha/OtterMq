package vhost

import (
	"sync/atomic"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence"
	jp "github.com/ottermq/ottermq/pkg/persistence/implementations/json"
)

// countingCollector counts RecordQueuePublish calls; everything else is a no-op.
type countingCollector struct {
	*metrics.MockCollector
	queuePublishes atomic.Int64
}

func (c *countingCollector) RecordQueuePublish(queueName string) {
	c.queuePublishes.Add(1)
}

func persistedQueueWithMessages(t *testing.T, vhostName, queueName string, n int) persistence.Persistence {
	t.Helper()
	p, err := jp.NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	if err := p.SaveQueueMetadata(vhostName, queueName, persistence.QueueProperties{Durable: true}); err != nil {
		t.Fatalf("SaveQueueMetadata: %v", err)
	}
	props := persistence.MessageProperties{DeliveryMode: uint8(amqp.PERSISTENT)}
	for i := range n {
		id := string(rune('a' + i))
		if err := p.SaveMessage(vhostName, queueName, id, []byte("x"), props); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}
	return p
}

// Regression test for #233: recovering a queue with persisted messages must
// not dereference a nil collector when none was passed in.
func TestNewVhost_RecoversPersistedMessagesWithoutCollector(t *testing.T) {
	p := persistedQueueWithMessages(t, "v", "q1", 2)

	vh := NewVhost("v", VHostOptions{QueueBufferSize: 10, Persistence: p})

	q, ok := vh.Queues["q1"]
	if !ok {
		t.Fatal("queue q1 was not recovered")
	}
	if got := q.Len(); got != 2 {
		t.Fatalf("recovered %d messages, want 2", got)
	}
	if vh.GetCollector() == nil {
		t.Fatal("collector is nil; NewVhost should default to a no-op collector")
	}
}

// The collector passed through VHostOptions is in place before recovery, so
// recovered messages are counted.
func TestNewVhost_CollectorFromOptionsSeesRecoveredMessages(t *testing.T) {
	p := persistedQueueWithMessages(t, "v", "q1", 3)
	c := &countingCollector{MockCollector: metrics.NewMockCollector(nil)}

	vh := NewVhost("v", VHostOptions{QueueBufferSize: 10, Persistence: p, Collector: c})

	if got := c.queuePublishes.Load(); got != 3 {
		t.Fatalf("RecordQueuePublish called %d times during recovery, want 3", got)
	}
	if vh.GetCollector() != c {
		t.Fatal("vhost does not use the collector from VHostOptions")
	}
}
