package e2e

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ottermq/ottermq/config"
	"github.com/ottermq/ottermq/internal/core/broker"
	"github.com/ottermq/ottermq/pkg/metrics"
	amqp "github.com/rabbitmq/amqp091-go"
)

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// startBrokerOnDataDir starts an in-process broker on its own port over
// dataDir and returns its AMQP URL and a stop function. The user database is
// the one TestMain opened.
//
// The broker has no exported way to close its listener, so after stop the
// old accept loop stays bound to its port until the test binary exits. A
// restart therefore uses a new port on the same data dir.
func startBrokerOnDataDir(t *testing.T, dataDir string) (string, func()) {
	t.Helper()
	port := freePort(t)
	cfg := &config.Config{
		BrokerHost:           "127.0.0.1",
		BrokerPort:           port,
		Version:              "test",
		QueueBufferSize:      1000,
		MaxPriority:          10,
		HeartbeatIntervalMax: 60,
		ChannelMax:           2048,
		FrameMax:             131072,
		DataDir:              dataDir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := broker.NewBroker(cfg, ctx, cancel, metrics.NewMockCollector(nil))
	go func() { _ = b.Start() }()
	select {
	case <-b.Ready:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("broker did not start")
	}

	stop := func() {
		cancel()
		b.BeginShutdown()
		b.BroadcastConnectionClose()
		b.Shutdown()
	}
	return fmt.Sprintf("amqp://guest:guest@127.0.0.1:%s/", port), stop
}

// Regression test for #233: persistent messages in a durable queue survive a
// broker restart on the same data dir and can be consumed afterwards.
func TestPersistentMessagesSurviveRestart(t *testing.T) {
	if testBroker == nil {
		t.Skip("needs the in-process user database; an external broker is in use")
	}
	dataDir := t.TempDir()
	const queue = "restart-durable-q"
	const count = 5

	url, stop := startBrokerOnDataDir(t, dataDir)
	conn, err := amqp.Dial(url)
	if err != nil {
		stop()
		t.Fatalf("dial first broker: %v", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	for i := range count {
		err := ch.PublishWithContext(context.Background(), "", queue, false, false, amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			Body:         fmt.Appendf(nil, "msg-%d", i),
		})
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	// Publisher confirms are not supported yet; a passive declare is a
	// synchronous round trip on the same channel, so the publishes before
	// it have been handled (and persisted) by the time it returns.
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("passive declare: %v", err)
	}
	if q.Messages != count {
		t.Fatalf("queue has %d messages before restart, want %d", q.Messages, count)
	}
	conn.Close()
	stop()

	// Restart: a new broker over the same data dir recovers the queue.
	url, stop = startBrokerOnDataDir(t, dataDir)
	defer stop()
	conn, err = amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial restarted broker: %v", err)
	}
	defer conn.Close()
	ch, err = conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	msgs, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	for i := range count {
		select {
		case d := <-msgs:
			if want := fmt.Sprintf("msg-%d", i); string(d.Body) != want {
				t.Fatalf("message %d: got %q, want %q", i, d.Body, want)
			}
			if err := d.Ack(false); err != nil {
				t.Fatalf("ack: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("received %d of %d messages after restart", i, count)
		}
	}
}
