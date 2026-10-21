package broker

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
)

func connectionCount(b *Broker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.Connections)
}

// waitNoConnections waits for the broker to deregister every connection.
// Cleanup runs right after the socket is closed, so the peer may see EOF
// first.
func waitNoConnections(t *testing.T, b *Broker, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for connectionCount(b) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections still registered after %s", connectionCount(b), within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHeartbeatTimeoutClosesSocket(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)

	// Negotiates heartbeat=2, so the broker gives up after about 4-5 s of
	// silence.
	_, tc := dialTap(t, addr, 2*time.Second)
	tc.mute.Store(true) // the client goes silent but keeps the socket open

	waitPeerClose(t, tc, 8*time.Second)
	waitNoConnections(t, b, time.Second)
}

func TestReadErrorClosesSocket(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)

	_, tc := dialTap(t, addr, testHeartbeat)
	// A one-byte method frame whose frame-end octet is 0x00 instead of 0xCE.
	badFrame := []byte{1, 0, 0, 0, 0, 0, 1, 0x00, 0x00}
	if _, err := tc.Conn.Write(badFrame); err != nil {
		t.Fatalf("write bad frame: %v", err)
	}

	waitPeerClose(t, tc, 3*time.Second)
	waitNoConnections(t, b, time.Second)
}

// The heartbeat monitor measured silence from the zero time, so a client
// that sent nothing during its first second was dropped. Once the socket is
// closed on heartbeat timeout that would disconnect it outright.
func TestQuietClientIsNotDroppedAfterOpen(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)

	// Heartbeat 10 s: amqp091 sends nothing for the next 5 s.
	conn, tc := dialTap(t, addr, testHeartbeat)
	time.Sleep(1500 * time.Millisecond)

	if n := connectionCount(b); n != 1 {
		t.Fatalf("%d connections registered after 1.5 s of silence, want 1", n)
	}
	select {
	case err := <-tc.readErr:
		t.Fatalf("socket ended with %v after 1.5 s of silence", err)
	default:
	}
	// A frame arriving now updates LastHeartbeat while the heartbeat monitor
	// reads it.
	roundTrip(t, conn)
}

func TestCleanupConnectionClosesSocketOnce(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	conn := &countingConn{Conn: server}

	b, _, _ := startTestBroker(t, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	connInfo := amqp.NewConnectionInfo(DEFAULT_VHOST)
	connInfo.Client = amqp.NewAmqpClient(conn, &amqp.AmqpClientConfig{}, ctx, cancel)
	if !b.registerConnection(conn, connInfo) {
		t.Fatal("registerConnection refused")
	}

	// Every teardown path ends here; they may race.
	b.cleanupConnection(conn)
	b.cleanupConnection(conn)
	b.connectionCloseOk(conn)
	connInfo.Client.CloseConn()

	if n := conn.closes.Load(); n != 1 {
		t.Errorf("Close called %d times, want 1", n)
	}
}

// countingConn counts Close calls.
type countingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}
