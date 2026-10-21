package amqp

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// panicConn is a net.Conn whose Write panics.
type panicConn struct{ net.Conn }

func (panicConn) Write([]byte) (int, error) { panic("injected write panic") }

func TestHeartbeatPanicCancelsOnlyThatConnection(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewAmqpClient(panicConn{server}, &AmqpClientConfig{HeartbeatInterval: 2}, ctx, cancel)
	c.LastHeartbeat = time.Now()
	c.StartHeartbeat()

	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("connection was not cancelled after the heartbeat goroutine panicked")
	}

	// The socket is closed too, so the peer sees EOF and the reader unblocks.
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("peer read returned %v, want EOF", err)
	}
}
