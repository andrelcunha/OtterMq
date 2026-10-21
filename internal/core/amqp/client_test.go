package amqp

import (
	"context"
	"io"
	"net"
	"sync/atomic"
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
	c.TouchHeartbeat()
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

// countingConn counts Close calls.
type countingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

func TestCloseConnClosesOnce(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	conn := &countingConn{Conn: server}

	ctx, cancel := context.WithCancel(context.Background())
	c := NewAmqpClient(conn, &AmqpClientConfig{}, ctx, cancel)
	for i := 0; i < 3; i++ {
		c.CloseConn()
	}

	if n := conn.closes.Load(); n != 1 {
		t.Errorf("Close called %d times, want 1", n)
	}
	if ctx.Err() == nil {
		t.Error("context not cancelled")
	}
}
