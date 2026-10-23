package broker

import (
	"context"
	"net"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/broker/vhost"
)

// remoteAddrConn gives a pipe its own remote address, from which the broker
// derives the connection ID.
type remoteAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.addr }

// The broker answers ConsumerCancelNotify per connection, from what the
// client advertised at connection setup, until the connection is cleaned up
// (#320).
func TestConsumerCancelNotifyPerConnection(t *testing.T) {
	b, _, _ := startTestBroker(t, nil, nil)
	register := func(port int, notify bool) net.Conn {
		server, client := net.Pipe()
		t.Cleanup(func() { client.Close() })
		conn := remoteAddrConn{Conn: server, addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}}
		ctx, cancel := context.WithCancel(context.Background())
		connInfo := amqp.NewConnectionInfo(DEFAULT_VHOST)
		connInfo.Client = amqp.NewAmqpClient(conn, &amqp.AmqpClientConfig{ConsumerCancelNotify: notify}, ctx, cancel)
		if !b.registerConnection(conn, connInfo) {
			t.Fatal("registerConnection refused")
		}
		return conn
	}
	idOf := func(conn net.Conn) vhost.ConnectionID {
		id, ok := b.GetConnectionID(conn)
		if !ok {
			t.Fatal("connection not registered")
		}
		return id
	}

	notifying := register(40001, true)
	silent := register(40002, false)
	notifyingID, silentID := idOf(notifying), idOf(silent)

	if !b.ConsumerCancelNotify(notifyingID) {
		t.Error("ConsumerCancelNotify = false for a client that advertised it")
	}
	if b.ConsumerCancelNotify(silentID) {
		t.Error("ConsumerCancelNotify = true for a client that didn't advertise it")
	}

	b.cleanupConnection(notifying)
	if b.ConsumerCancelNotify(notifyingID) {
		t.Error("ConsumerCancelNotify = true after the connection was cleaned up")
	}
}
