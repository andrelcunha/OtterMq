package broker

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/models"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

// Publishing (routed and unroutable), opening and closing channels, and the
// management listings run at the same time. Every shared broker map is read
// and written under its lock, so -race reports nothing.
func TestBroker_PublishWhileListingIsRaceFree(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		conn, err := amqp091.Dial("amqp://guest:guest@" + addr + "/")
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })

		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				ch, err := conn.Channel()
				if err != nil {
					t.Errorf("channel: %v", err)
					return
				}
				q, err := ch.QueueDeclare("", false, true, true, false, nil)
				if err != nil {
					t.Errorf("queue.declare: %v", err)
					return
				}
				for j := 0; j < 20; j++ {
					_ = ch.Publish("", q.Name, false, false, amqp091.Publishing{Body: []byte("routed")})
					_ = ch.Publish("", "no-such-queue", false, false, amqp091.Publishing{Body: []byte("dropped")})
				}
				_ = ch.Close()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_, _ = b.ListChannels("")
			_ = models.MapListConnectionsDTO(b.ListConnections())
			_ = b.GetObjectTotalsOverview()
			_ = b.GetOverviewConnStats()
		}
	}()
	wg.Wait()
}

// A connection can be removed by another goroutine (management close,
// heartbeat timeout) while its handlers still run. The helpers treat a
// missing connection as gone instead of dereferencing a nil entry.
func TestBroker_HelpersTolerateRemovedConnection(t *testing.T) {
	b := NewBrokerForVHosts()
	gone, peer := net.Pipe()
	t.Cleanup(func() { gone.Close(); peer.Close() })
	open := &amqp.RequestMethodMessage{Channel: 1}

	for name, call := range map[string]func(){
		"setConnectionClosingState": func() { b.setConnectionClosingState(gone) },
		"getCurrentState":           func() { b.getCurrentState(gone, 1) },
		"checkChannel":              func() { b.checkChannel(gone, 1) },
		"registerChannel":           func() { b.registerChannel(gone, open) },
		"removeChannel":             func() { b.removeChannel(gone, 1) },
		"resetChannelState":         func() { b.resetChannelState(gone, 1) },
		"connUser":                  func() { b.connUser(gone) },
		"CloseConnection":           func() { _ = b.CloseConnection("127.0.0.1:1", "test") },
		"CreateChannelInfo":         func() { _, _ = b.CreateChannelInfo("127.0.0.1:1", 1, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked: %v", r)
				}
			}()
			call()
		})
	}
}
