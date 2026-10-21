package vhost

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/pkg/metrics"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
)

// discardSender is a FrameSender that drops every frame. Safe for
// concurrent use by delivery loops.
type discardSender struct{ frames atomic.Int64 }

func (s *discardSender) SendFrame(ConnectionID, uint16, []byte) error {
	s.frames.Add(1)
	return nil
}

func newRaceTestVHost(t *testing.T) *VHost {
	t.Helper()
	vh := NewVhost("race-vhost", VHostOptions{
		Persistence:     &dummy.DummyPersistence{},
		QueueBufferSize: 1000,
	})
	vh.SetMetricsCollector(metrics.NewMockCollector(nil))
	vh.SetFramer(&amqp.DefaultFramer{})
	vh.SetFrameSender(&discardSender{})
	return vh
}

// HandleBasicQos used to write the QoS fields under vh.mu only, while the
// delivery loop read them in shouldThrottle under no lock (#217).
func TestHandleBasicQosDuringDeliveryIsRaceFree(t *testing.T) {
	vh := newRaceTestVHost(t)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)

	if err := vh.HandleBasicQos(connID, 1, 100, false); err != nil {
		t.Fatalf("qos: %v", err)
	}
	if _, err := vh.RegisterConsumer(NewConsumer(connID, 1, "q", "ctag", &ConsumerProperties{NoAck: false})); err != nil {
		t.Fatalf("register consumer: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		q.Push(Message{ID: fmt.Sprintf("m%d", i)})
		if err := vh.HandleBasicQos(connID, 1, uint16(i%50+1), true); err != nil {
			t.Fatalf("qos: %v", err)
		}
	}
}

// CancelConsumer used to clear Consumer.Active under vh.mu while the delivery
// loop read it in deliverToConsumer under no lock (#217). The delivery loop
// holds a consumer picked by GetActiveConsumersForQueue, so the consumer can
// be cancelled between that pick and the delivery; deliverToConsumer is
// driven directly here to make that window wide.
func TestCancelConsumerDuringDeliveryIsRaceFree(t *testing.T) {
	vh := newRaceTestVHost(t)
	connID := newTestConsumerConnID()
	q, err := vh.CreateQueue("q", &QueueProperties{Arguments: map[string]any{}}, connID)
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	t.Cleanup(q.stopDeliveryLoop)

	consumer := NewConsumer(connID, 1, "q", "ctag", &ConsumerProperties{NoAck: true})
	if _, err := vh.RegisterConsumer(consumer); err != nil {
		t.Fatalf("register consumer: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Fails while the consumer is cancelled; that is expected.
			_ = vh.deliverToConsumer(consumer, Message{ID: "m"}, false)
		}
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := vh.CancelConsumer(1, "ctag"); err != nil {
			t.Fatalf("cancel consumer: %v", err)
		}
		if _, err := vh.RegisterConsumer(consumer); err != nil {
			t.Fatalf("register consumer: %v", err)
		}
	}
	close(stop)
	<-done
}
