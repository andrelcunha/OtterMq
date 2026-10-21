package broker

import (
	"net"
	"strings"
	"testing"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

func TestFrameLoopPanicClosesOnlyThatConnection(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)

	healthy, _ := dialTap(t, addr, 0)
	victimConn, victim := dialTap(t, addr, 0)
	closed := victimConn.NotifyClose(make(chan *amqp091.Error, 1))

	if _, err := victim.Conn.Write(poisonFrame); err != nil {
		t.Fatalf("write poison frame: %v", err)
	}
	waitPeerClose(t, victim, 3*time.Second)
	if err := <-closed; err == nil || err.Code != amqp091.InternalError {
		t.Errorf("victim closed with %v, want connection.close %d", err, amqp091.InternalError)
	}

	// The other client keeps publishing and consuming.
	roundTrip(t, healthy)

	logs := testLogs.String()
	if !strings.Contains(logs, "injected frame panic") || !strings.Contains(logs, "goroutine ") {
		t.Errorf("recovered panic was not logged with a stack trace")
	}
}

func TestHandshakePanicKeepsAccepting(t *testing.T) {
	_, ff, addr := startTestBroker(t, nil, nil)

	ff.panicNextHandshake.Store(true)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	if err := readUntilClosed(raw, 3*time.Second); !isClosedByPeer(err) {
		t.Fatalf("socket ended with %v, want EOF/RST from the broker", err)
	}

	// The broker still accepts and serves new connections.
	conn, _ := dialTap(t, addr, 0)
	roundTrip(t, conn)
}
