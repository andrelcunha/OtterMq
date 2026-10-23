package vhost

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
	"github.com/rs/zerolog"
)

// muteLogs disables logging for the rest of the benchmark: publish and
// delivery log at debug level on every message.
func muteLogs(b *testing.B) {
	b.Helper()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.Disabled)
	b.Cleanup(func() { zerolog.SetGlobalLevel(level) })
}

// newBenchVHost returns a vhost with no-op persistence, the mock metrics
// collector and the real framer. queueBuffer is each queue's channel size.
func newBenchVHost(b *testing.B, queueBuffer int, sender FrameSender) *VHost {
	b.Helper()
	vh := NewVhost("bench", VHostOptions{
		Persistence:     &dummy.DummyPersistence{},
		QueueBufferSize: queueBuffer,
	})
	vh.SetMetricsCollector(metrics.NewMockCollector(nil))
	vh.SetFramer(&amqp.DefaultFramer{})
	if sender != nil {
		vh.SetFrameSender(sender)
	}
	return vh
}

func BenchmarkMatchTopic(b *testing.B) {
	cases := []struct{ name, rk, pattern string }{
		{"literal", "stock.usd.nyse", "stock.usd.nyse"},
		{"star", "stock.usd.nyse", "stock.*.nyse"},
		{"hash", "a.b.c.d.e.f", "a.#.f"},
		{"no_match", "stock.usd.nyse", "stock.eur.*"},
		{"pathological", strings.TrimSuffix(strings.Repeat("a.", 24), "."), "#.#.#.#.#.#.x"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				MatchTopic(c.rk, c.pattern)
			}
		})
	}
}

// BenchmarkVHostPublish routes one message per iteration through an exchange
// with `bindings` bindings, of which `targets` match (one queue each). The
// rest bind a separate queue with keys that don't match.
func BenchmarkVHostPublish(b *testing.B) {
	type shape struct {
		typ      ExchangeType
		bindings int
		targets  int
	}
	var shapes []shape
	for _, typ := range []ExchangeType{DIRECT, TOPIC} {
		for _, bindings := range []int{1, 10, 1000} {
			for _, targets := range []int{1, 10} {
				if targets <= bindings {
					shapes = append(shapes, shape{typ, bindings, targets})
				}
			}
		}
	}
	// Every fanout binding is a target.
	shapes = append(shapes, shape{FANOUT, 1, 1}, shape{FANOUT, 10, 10})

	const purgeEvery = 1024
	for _, s := range shapes {
		name := fmt.Sprintf("%s/bindings=%d/queues=%d", s.typ, s.bindings, s.targets)
		b.Run(name, func(b *testing.B) {
			muteLogs(b)
			vh := newBenchVHost(b, purgeEvery+1, nil)
			const ex = "bench.ex"
			if err := vh.CreateExchange(ex, s.typ, &ExchangeProperties{Arguments: map[string]any{}}); err != nil {
				b.Fatal(err)
			}
			routingKey, matchKey := "bench.key", "bench.key"
			if s.typ == TOPIC {
				routingKey, matchKey = "bench.usd.key", "bench.*.key"
			}

			queues := make([]string, 0, s.targets+1)
			for i := 0; i < s.targets; i++ {
				q := fmt.Sprintf("target-%d", i)
				if _, err := vh.CreateQueue(q, &QueueProperties{Arguments: map[string]any{}}, INTERNAL_CONN_ID); err != nil {
					b.Fatal(err)
				}
				if err := vh.BindQueue(ex, q, matchKey, nil, INTERNAL_CONN_ID); err != nil {
					b.Fatal(err)
				}
				queues = append(queues, q)
			}
			if others := s.bindings - s.targets; others > 0 {
				if _, err := vh.CreateQueue("other", &QueueProperties{Arguments: map[string]any{}}, INTERNAL_CONN_ID); err != nil {
					b.Fatal(err)
				}
				for i := 0; i < others; i++ {
					key := fmt.Sprintf("other.%d.key", i)
					if err := vh.BindQueue(ex, "other", key, nil, INTERNAL_CONN_ID); err != nil {
						b.Fatal(err)
					}
				}
				queues = append(queues, "other")
			}

			msg := &Message{ID: "bench", Body: make([]byte, 1024), RoutingKey: routingKey}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := vh.Publish(ex, routingKey, msg); err != nil {
					b.Fatal(err)
				}
				// Queues have no consumers: empty them before they fill up.
				if i%purgeEvery == purgeEvery-1 {
					b.StopTimer()
					for _, q := range queues {
						if _, err := vh.PurgeQueue(q, INTERNAL_CONN_ID); err != nil {
							b.Fatal(err)
						}
					}
					b.StartTimer()
				}
			}
		})
	}
}

// countingSender is a FrameSender that discards frames and counts deliveries
// (deliverToConsumer sends each delivery as one SendFrame call). With acks
// set, it hands each delivery's channel to an acker goroutine.
type countingSender struct {
	delivered atomic.Int64
	acks      chan ConnectionChannelKey
}

func (s *countingSender) SendFrame(connID ConnectionID, channel uint16, _ []byte) error {
	s.delivered.Add(1)
	if s.acks != nil {
		s.acks <- ConnectionChannelKey{connID, channel}
	}
	return nil
}

func (*countingSender) ConsumerCancelNotify(ConnectionID) bool { return false }

// BenchmarkPublishDeliverAck publishes b.N 1 KiB messages from parallel
// publishers into one queue and waits until every one has been delivered
// (and, for manual ack, acked) through the real delivery loop.
func BenchmarkPublishDeliverAck(b *testing.B) {
	type mode struct {
		name      string
		noAck     bool
		prefetch  uint16
		consumers int
	}
	var modes []mode
	for _, consumers := range []int{1, 4} {
		modes = append(modes, mode{fmt.Sprintf("auto-ack/consumers=%d", consumers), true, 0, consumers})
		for _, prefetch := range []uint16{1, 100, 0} {
			modes = append(modes, mode{fmt.Sprintf("manual-ack/prefetch=%d/consumers=%d", prefetch, consumers), false, prefetch, consumers})
		}
	}

	for _, m := range modes {
		b.Run(m.name, func(b *testing.B) {
			muteLogs(b)
			sender := &countingSender{}
			if !m.noAck {
				sender.acks = make(chan ConnectionChannelKey, 1<<16)
			}
			// Publishers can run ahead of delivery; the queue must hold all
			// of b.N or Push drops messages and the wait below never ends.
			vh := newBenchVHost(b, b.N+1, sender)
			connID := ConnectionID("bench-conn")
			q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
			if err != nil {
				b.Fatal(err)
			}
			// The broker does this on queue.declare; CreateQueue does not.
			if err := vh.BindToDefaultExchange("q"); err != nil {
				b.Fatal(err)
			}

			var acked atomic.Int64
			ackerDone := make(chan struct{})
			if !m.noAck {
				go func() {
					defer close(ackerDone)
					// Delivery tags are per channel and sequential, and one
					// delivery loop serves the queue, so they arrive in order.
					tags := make(map[ConnectionChannelKey]uint64)
					for key := range sender.acks {
						tags[key]++
						if err := vh.HandleBasicAck(key.ConnectionID, key.Channel, tags[key], false); err != nil {
							b.Error(err)
						}
						acked.Add(1)
					}
				}()
				b.Cleanup(func() { close(sender.acks); <-ackerDone })
			}
			// Cleanups run last-in first-out: the delivery loop stops before
			// the acks channel it sends on is closed.
			b.Cleanup(q.stopDeliveryLoop)

			for c := 0; c < m.consumers; c++ {
				channel := uint16(c + 1)
				if m.prefetch > 0 {
					if err := vh.HandleBasicQos(connID, channel, m.prefetch, false); err != nil {
						b.Fatal(err)
					}
				}
				consumer := NewConsumer(connID, channel, "q", fmt.Sprintf("ctag-%d", c), &ConsumerProperties{NoAck: m.noAck})
				if _, err := vh.RegisterConsumer(consumer); err != nil {
					b.Fatal(err)
				}
			}

			body := make([]byte, 1024)
			done := func() bool {
				if m.noAck {
					return sender.delivered.Load() >= int64(b.N)
				}
				return acked.Load() >= int64(b.N)
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					msg := &Message{ID: "bench", Body: body, RoutingKey: "q"}
					if _, err := vh.Publish("", "q", msg); err != nil {
						b.Error(err)
						return
					}
				}
			})
			deadline := time.Now().Add(30 * time.Second)
			for !done() {
				if time.Now().After(deadline) {
					b.Fatalf("delivered %d, acked %d of %d messages", sender.delivered.Load(), acked.Load(), b.N)
				}
				time.Sleep(50 * time.Microsecond)
			}
			b.StopTimer()
		})
	}
}
