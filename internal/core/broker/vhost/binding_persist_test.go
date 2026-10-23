package vhost

import (
	"testing"

	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence"
	jp "github.com/ottermq/ottermq/pkg/persistence/implementations/json"
)

// newDurableBindingVhost returns a vhost on a JSON store in a temp dir, with
// a durable queue "q" and a durable direct exchange "ex" that has arguments
// of its own, plus a function that recovers a fresh vhost from the same store.
func newDurableBindingVhost(t *testing.T) (*VHost, func() *VHost) {
	t.Helper()
	p, err := jp.NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	recover := func() *VHost {
		return NewVhost("v", VHostOptions{
			QueueBufferSize: 10,
			Persistence:     p,
			Collector:       metrics.NewMockCollector(nil),
		})
	}
	vh := recover()
	if _, err := vh.CreateQueue("q", &QueueProperties{Durable: true}, newTestConsumerConnID()); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	exProps := &ExchangeProperties{Durable: true, Arguments: map[string]any{"alternate-exchange": "ae"}}
	if err := vh.CreateExchange("ex", DIRECT, exProps); err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	return vh, recover
}

func bindingsTo(vh *VHost, exchange, queue, rk string) []*Binding {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	var out []*Binding
	for _, b := range vh.Exchanges[exchange].Bindings[rk] {
		if b.Queue.Name == queue {
			out = append(out, b)
		}
	}
	return out
}

// Regression test for #298: an unbind of a durable binding is persisted, so
// the binding doesn't come back when the vhost is recovered.
func TestUnbindQueue_StaysUnboundAfterRecovery(t *testing.T) {
	cases := []struct {
		name       string
		bindArgs   map[string]any
		unbindArgs map[string]any
	}{
		{"no arguments", nil, map[string]any{}},
		{"empty table", map[string]any{}, map[string]any{}},
		{"with arguments", map[string]any{"x-b": "B"}, map[string]any{"x-b": "B"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vh, recover := newDurableBindingVhost(t)
			if err := vh.BindQueue("ex", "q", "k", tc.bindArgs, INTERNAL_CONN_ID); err != nil {
				t.Fatalf("BindQueue: %v", err)
			}
			if err := vh.UnbindQueue("ex", "q", "k", tc.unbindArgs, INTERNAL_CONN_ID); err != nil {
				t.Fatalf("UnbindQueue: %v", err)
			}
			if got := bindingsTo(recover(), "ex", "q", "k"); len(got) != 0 {
				t.Fatalf("binding came back after recovery with args %v", got[0].Args)
			}
		})
	}
}

// Regression test for #298: a durable binding is persisted and recovered with
// its own arguments, not its exchange's, and can be unbound by them after
// the recovery (numbers come back from JSON as float64).
func TestBindQueue_RecoversBindingArguments(t *testing.T) {
	vh, recover := newDurableBindingVhost(t)
	args := map[string]any{"x-b": "B", "n": int32(5)}
	if err := vh.BindQueue("ex", "q", "k", args, INTERNAL_CONN_ID); err != nil {
		t.Fatalf("BindQueue: %v", err)
	}

	recovered := recover()
	got := bindingsTo(recovered, "ex", "q", "k")
	if len(got) != 1 {
		t.Fatalf("recovered %d bindings, want 1", len(got))
	}
	if !bindingArgumentsMatch(got[0].Args, args) {
		t.Fatalf("recovered binding args %v, want %v", got[0].Args, args)
	}

	if err := recovered.UnbindQueue("ex", "q", "k", args, INTERNAL_CONN_ID); err != nil {
		t.Fatalf("UnbindQueue after recovery: %v", err)
	}
	if got := bindingsTo(recover(), "ex", "q", "k"); len(got) != 0 {
		t.Fatalf("binding came back after a second recovery with args %v", got[0].Args)
	}
}

// Data written before #298 holds each binding with its exchange's arguments.
// For an exchange without arguments that is null, and a client's unbind,
// which sends an empty table, still removes it, in memory and on disk.
func TestUnbindQueue_RemovesBindingSavedWithNullArguments(t *testing.T) {
	p, err := jp.NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	if err := p.SaveQueueMetadata("v", "q", persistence.QueueProperties{Durable: true}); err != nil {
		t.Fatalf("SaveQueueMetadata: %v", err)
	}
	if err := p.SaveExchangeMetadata("v", "ex", string(DIRECT), persistence.ExchangeProperties{Durable: true}); err != nil {
		t.Fatalf("SaveExchangeMetadata: %v", err)
	}
	if err := p.SaveBindingState("v", "ex", "q", "k", nil); err != nil {
		t.Fatalf("SaveBindingState: %v", err)
	}
	recover := func() *VHost {
		return NewVhost("v", VHostOptions{QueueBufferSize: 10, Persistence: p, Collector: metrics.NewMockCollector(nil)})
	}

	vh := recover()
	if got := bindingsTo(vh, "ex", "q", "k"); len(got) != 1 {
		t.Fatalf("recovered %d bindings, want 1", len(got))
	}
	if err := vh.UnbindQueue("ex", "q", "k", map[string]any{}, INTERNAL_CONN_ID); err != nil {
		t.Fatalf("UnbindQueue: %v", err)
	}
	if got := bindingsTo(recover(), "ex", "q", "k"); len(got) != 0 {
		t.Fatalf("binding came back after recovery with args %v", got[0].Args)
	}
}
