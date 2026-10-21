package broker

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

func TestIdleConnectionDoesNotBlockHandshakes(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)

	idle, err := net.Dial("tcp", addr) // never writes
	if err != nil {
		t.Fatalf("dial idle: %v", err)
	}
	defer idle.Close()

	start := time.Now()
	conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/",
		amqp091.Config{Dial: amqp091.DefaultDial(3 * time.Second)})
	if err != nil {
		t.Fatalf("handshake blocked by an idle connection: %v", err)
	}
	defer conn.Close()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("handshake took %s with an idle connection open, want < 1s", elapsed)
	}
}

func TestBadProtocolHeaderClosesSocket(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("AMQP\x01\x01\x08\x00")); err != nil {
		t.Fatalf("write header: %v", err)
	}

	// The broker answers with its own header and then closes the socket.
	_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, 8)
	if _, err := io.ReadFull(raw, header); err != nil {
		t.Fatalf("read server header: %v", err)
	}
	if string(header) != "AMQP\x00\x00\x09\x01" {
		t.Errorf("server header = %q, want AMQP 0-9-1", header)
	}
	if err := readUntilClosed(raw, 2*time.Second); !isClosedByPeer(err) {
		t.Fatalf("socket ended with %v, want EOF/RST from the broker", err)
	}
}

// Handshakes now run concurrently, so they must not share the tune values a
// previous client sent (the shared map was also a data race under -race).
func TestHandshakesOfferConfiguredTuning(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)

	dial := func(hb time.Duration) *amqp091.Connection {
		conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/", amqp091.Config{Heartbeat: hb})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}

	dial(5 * time.Second) // tune-ok heartbeat=5
	// The broker offers 60; amqp091 picks the smaller of offer and its own 30.
	if got := dial(30 * time.Second).Config.Heartbeat; got != 30*time.Second {
		t.Errorf("second client negotiated heartbeat %s, want 30s (the first client's value leaked)", got)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(hb time.Duration) {
			defer wg.Done()
			conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/", amqp091.Config{Heartbeat: hb})
			if err != nil {
				t.Errorf("concurrent dial: %v", err)
				return
			}
			conn.Close()
		}(time.Duration(10+i) * time.Second)
	}
	wg.Wait()
}

func TestShutdownWaitsForHandshakesInFlight(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("AMQP\x00\x00\x09\x01")); err != nil {
		t.Fatalf("write header: %v", err)
	}
	// Reading connection.start proves the handshake is in progress.
	_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := raw.Read(make([]byte, 7)); err != nil {
		t.Fatalf("read connection.start: %v", err)
	}

	waited := make(chan struct{})
	go func() {
		b.ActiveConns.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("ActiveConns.Wait returned while a handshake was still in progress")
	case <-time.After(200 * time.Millisecond):
	}

	raw.Close() // the handshake fails, which ends it
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("ActiveConns.Wait did not return after the handshake ended")
	}
}

func TestHandshakeFinishingDuringShutdownIsNotRegistered(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, func(b *Broker, ff *faultyFramer) {
		ff.afterHandshake = b.BeginShutdown
	})

	conn, tc := dialTap(t, addr, testHeartbeat)
	waitPeerClose(t, tc, 3*time.Second)
	_ = conn

	b.mu.Lock()
	n := len(b.Connections)
	b.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d connections registered after shutdown began, want 0", n)
	}
}
