package broker

import (
	"net"
	"testing"
	"time"

	"github.com/ottermq/ottermq/config"
)

func TestHandshakeTimeoutClosesSocket(t *testing.T) {
	_, _, addr := startTestBroker(t, func(c *config.Config) {
		c.HandshakeTimeout = 300 * time.Millisecond
	}, nil)

	cases := map[string][]byte{
		"silent":         nil,
		"header only":    []byte("AMQP\x00\x00\x09\x01"),
		"partial header": []byte("AM"),
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer raw.Close()
			if _, err := raw.Write(send); err != nil {
				t.Fatalf("write: %v", err)
			}
			start := time.Now()
			if err := readUntilClosed(raw, 3*time.Second); !isClosedByPeer(err) {
				t.Fatalf("socket ended with %v, want EOF/RST from the broker", err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("closed after %s, want about 300ms", elapsed)
			}
		})
	}
}

// Kept under one second on purpose: the broker's heartbeat monitor first runs
// a second after the handshake, and its unsynchronised LastHeartbeat read
// (#217) would turn the run red under -race.
func TestHandshakeTimeoutIsClearedAfterOpen(t *testing.T) {
	_, _, addr := startTestBroker(t, func(c *config.Config) {
		c.HandshakeTimeout = 100 * time.Millisecond
	}, nil)

	conn, _ := dialTap(t, addr, testHeartbeat)
	time.Sleep(400 * time.Millisecond) // well past the handshake timeout
	roundTrip(t, conn)
}
