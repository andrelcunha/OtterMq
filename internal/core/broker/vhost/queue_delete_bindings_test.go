package vhost

import (
	"testing"

	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
)

// Regression test for #230: deleting a queue must remove its bindings from
// every exchange type, all of them (several keys, several argument sets),
// and leave other queues' bindings alone.
func TestDeleteQueue_RemovesBindingsFromAllExchangeTypes(t *testing.T) {
	vh := NewVhost("/", VHostOptions{
		QueueBufferSize: 10,
		Persistence:     &dummy.DummyPersistence{},
		Collector:       metrics.NewMockCollector(nil),
	})
	connID := newTestConsumerConnID()
	for _, name := range []string{"gone", "kept"} {
		if _, err := vh.CreateQueue(name, &QueueProperties{}, connID); err != nil {
			t.Fatalf("CreateQueue %s: %v", name, err)
		}
	}
	exchanges := map[string]ExchangeType{"ex-direct": DIRECT, "ex-fanout": FANOUT, "ex-topic": TOPIC}
	for name, typ := range exchanges {
		if err := vh.CreateExchange(name, typ, &ExchangeProperties{}); err != nil {
			t.Fatalf("CreateExchange %s: %v", name, err)
		}
	}
	bind := func(ex, q, rk string, args map[string]any) {
		t.Helper()
		if err := vh.BindQueue(ex, q, rk, args, connID); err != nil {
			t.Fatalf("BindQueue %s -> %s (%q): %v", ex, q, rk, err)
		}
	}
	bind("ex-direct", "gone", "rk", nil)
	bind("ex-direct", "gone", "rk", map[string]any{"x": "second"})
	bind("ex-direct", "kept", "rk", nil)
	bind("ex-fanout", "gone", "", nil)
	bind("ex-fanout", "kept", "", nil)
	bind("ex-topic", "gone", "a.#", nil)
	bind("ex-topic", "gone", "*.b", nil)
	bind("ex-topic", "kept", "a.#", nil)
	bind(MANDATORY_TOPIC, "gone", "a.#", nil)

	if err := vh.DeleteQueuebyName("gone"); err != nil {
		t.Fatalf("DeleteQueuebyName: %v", err)
	}

	vh.mu.Lock()
	defer vh.mu.Unlock()
	keptBindings := 0
	for exName, ex := range vh.Exchanges {
		for rk, bindings := range ex.Bindings {
			if len(bindings) == 0 {
				t.Errorf("exchange %s keeps an empty binding list for %q", exName, rk)
			}
			for _, b := range bindings {
				if b.Queue.Name == "gone" {
					t.Errorf("exchange %s (%s) still binds the deleted queue with key %q", exName, ex.Typ, rk)
				}
				if b.Queue.Name == "kept" {
					keptBindings++
				}
			}
		}
	}
	if keptBindings != 3 {
		t.Errorf("other queue's bindings: got %d, want 3 (one per test exchange)", keptBindings)
	}
}

func TestDeleteQueue_AutoDeleteTopicExchange(t *testing.T) {
	vh := NewVhost("/", VHostOptions{
		QueueBufferSize: 10,
		Persistence:     &dummy.DummyPersistence{},
	})
	connID := newTestConsumerConnID()
	if _, err := vh.CreateQueue("q", &QueueProperties{}, connID); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := vh.CreateExchange("ex-topic", TOPIC, &ExchangeProperties{AutoDelete: true}); err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	if err := vh.BindQueue("ex-topic", "q", "a.#", nil, connID); err != nil {
		t.Fatalf("BindQueue: %v", err)
	}

	if err := vh.DeleteQueuebyName("q"); err != nil {
		t.Fatalf("DeleteQueuebyName: %v", err)
	}
	if _, exists := vh.Exchanges["ex-topic"]; exists {
		t.Error("auto-delete topic exchange should be deleted with its last binding")
	}
}

// After the delete, a routing key that only matched the deleted queue's
// topic binding must be unroutable (mandatory publishes are returned).
func TestDeleteQueue_TopicRoutingKeyBecomesUnroutable(t *testing.T) {
	vh := NewVhost("/", VHostOptions{
		QueueBufferSize: 10,
		Persistence:     &dummy.DummyPersistence{},
	})
	connID := newTestConsumerConnID()
	if _, err := vh.CreateQueue("q", &QueueProperties{}, connID); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := vh.BindQueue(MANDATORY_TOPIC, "q", "a.#", nil, connID); err != nil {
		t.Fatalf("BindQueue: %v", err)
	}
	if err := vh.DeleteQueuebyName("q"); err != nil {
		t.Fatalf("DeleteQueuebyName: %v", err)
	}
	routable, err := vh.HasRoutingForMessage(MANDATORY_TOPIC, "a.b")
	if err != nil {
		t.Fatalf("HasRoutingForMessage: %v", err)
	}
	if routable {
		t.Error("routing key a.b is still routable through the deleted queue's binding")
	}
}
