package broker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/pkg/metrics"
)

// A client that has stopped reading must not hold up the shutdown
// connection.close broadcast, nor keep b.mu while the broker waits on it.
func TestBroadcastConnectionClose_StuckClientDoesNotHoldBrokerLock(t *testing.T) {
	b := &Broker{
		framer:    &amqp.DefaultFramer{},
		connInfos: make(map[net.Conn]*amqp.ConnectionInfo),
		collector: metrics.NewMockCollector(nil),
	}

	// net.Pipe has no buffer: a write blocks until the other end reads, which
	// the stuck client never does.
	stuck, stuckPeer := net.Pipe()
	t.Cleanup(func() { stuck.Close(); stuckPeer.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stuckClient := amqp.NewAmqpClient(stuck, &amqp.AmqpClientConfig{}, ctx, cancel)

	// A healthy client next to it still gets its connection.close.
	healthy, healthyPeer := net.Pipe()
	t.Cleanup(func() { healthy.Close(); healthyPeer.Close() })
	received := make(chan error, 1)
	go func() {
		_, err := (&amqp.DefaultFramer{}).ReadFrame(healthyPeer, 131072)
		received <- err
	}()

	b.mu.Lock()
	b.connInfos[stuck] = &amqp.ConnectionInfo{Client: stuckClient}
	b.connInfos[healthy] = &amqp.ConnectionInfo{}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.BroadcastConnectionClose()
	}()

	// While the broadcast waits on the stuck client, b.mu is free.
	time.Sleep(100 * time.Millisecond)
	locked := make(chan struct{})
	go func() {
		b.mu.Lock()
		b.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(500 * time.Millisecond):
		t.Error("b.mu is held while connection.close is written to a client that doesn't read")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Unblock the broadcast so the test can finish.
		stuckPeer.Close()
		<-done
		t.Fatal("BroadcastConnectionClose did not return within 5s with a client that doesn't read")
	}
	if err := <-received; err != nil {
		t.Errorf("healthy client: reading connection.close: %v", err)
	}
	if stuckClient.Ctx.Err() == nil {
		t.Error("the stuck client's connection was not closed after its write timed out")
	}
}
