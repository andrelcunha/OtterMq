package broker

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

// tuneOk holds the connection.tune-ok fields a test may rewrite.
type tuneOk struct {
	channelMax uint16
	frameMax   uint32
	heartbeat  uint16
}

// tuningConn is the client side of an amqp091 connection. It rewrites the
// client's connection.tune-ok on the way out, so a test can send values
// amqp091 never would, and records the frames the broker sends.
type tuningConn struct {
	net.Conn
	rewrite func(*tuneOk)
	mute    atomic.Bool // drop everything the client writes

	wmu         sync.Mutex
	wbuf        []byte
	headerSent  bool
	passthrough bool

	rmu        sync.Mutex
	rbuf       []byte
	maxBody    int // largest body frame received, header and frame-end included
	heartbeats int
	closeCodes []uint16 // reply codes of connection.close frames received
}

func (c *tuningConn) Write(p []byte) (int, error) {
	if c.mute.Load() {
		return len(p), nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.passthrough {
		return c.Conn.Write(p)
	}
	c.wbuf = append(c.wbuf, p...)
	var out []byte
	if !c.headerSent && len(c.wbuf) >= 8 {
		out = append(out, c.wbuf[:8]...)
		c.wbuf = c.wbuf[8:]
		c.headerSent = true
	}
	for c.headerSent && !c.passthrough && len(c.wbuf) >= 7 {
		size := int(binary.BigEndian.Uint32(c.wbuf[3:7]))
		if len(c.wbuf) < 7+size+1 {
			break
		}
		frame := c.wbuf[:7+size+1]
		payload := frame[7 : 7+size]
		if frame[0] == byte(amqp.TYPE_METHOD) && size >= 12 &&
			binary.BigEndian.Uint16(payload[0:2]) == uint16(amqp.CONNECTION) &&
			binary.BigEndian.Uint16(payload[2:4]) == uint16(amqp.CONNECTION_TUNE_OK) {
			t := tuneOk{
				channelMax: binary.BigEndian.Uint16(payload[4:6]),
				frameMax:   binary.BigEndian.Uint32(payload[6:10]),
				heartbeat:  binary.BigEndian.Uint16(payload[10:12]),
			}
			if c.rewrite != nil {
				c.rewrite(&t)
			}
			binary.BigEndian.PutUint16(payload[4:6], t.channelMax)
			binary.BigEndian.PutUint32(payload[6:10], t.frameMax)
			binary.BigEndian.PutUint16(payload[10:12], t.heartbeat)
			c.passthrough = true
		}
		out = append(out, frame...)
		c.wbuf = c.wbuf[len(frame):]
	}
	if c.passthrough {
		out = append(out, c.wbuf...)
		c.wbuf = nil
	}
	if len(out) > 0 {
		if _, err := c.Conn.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (c *tuningConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.rmu.Lock()
	defer c.rmu.Unlock()
	c.rbuf = append(c.rbuf, p[:n]...)
	for len(c.rbuf) >= 7 {
		size := int(binary.BigEndian.Uint32(c.rbuf[3:7]))
		if len(c.rbuf) < 7+size+1 {
			break
		}
		payload := c.rbuf[7 : 7+size]
		switch c.rbuf[0] {
		case byte(amqp.TYPE_METHOD):
			if size >= 6 && binary.BigEndian.Uint16(payload[0:2]) == uint16(amqp.CONNECTION) &&
				binary.BigEndian.Uint16(payload[2:4]) == uint16(amqp.CONNECTION_CLOSE) {
				c.closeCodes = append(c.closeCodes, binary.BigEndian.Uint16(payload[4:6]))
			}
		case byte(amqp.TYPE_BODY):
			c.maxBody = max(c.maxBody, 7+size+1)
		case byte(amqp.TYPE_HEARTBEAT):
			c.heartbeats++
		}
		c.rbuf = c.rbuf[7+size+1:]
	}
	return n, err
}

func (c *tuningConn) stats() (maxBody, heartbeats int) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return c.maxBody, c.heartbeats
}

func (c *tuningConn) receivedCloseCodes() []uint16 {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return append([]uint16(nil), c.closeCodes...)
}

// silence stops the client from sending anything more, then closes the
// socket. Tests that outlive the heartbeat monitor's first tick call it so no
// frame reaches the broker while the monitor reads LastHeartbeat (#217).
func (c *tuningConn) silence() {
	c.mute.Store(true)
	c.Conn.Close()
}

// dialTuning opens an amqp091 connection through a tuningConn; frameSize is
// amqp091's own frame-max (0: take the server's). It returns the dial error
// instead of failing, for tests where the broker must refuse.
func dialTuning(t *testing.T, addr string, frameSize int, rewrite func(*tuneOk)) (*amqp091.Connection, *tuningConn, error) {
	t.Helper()
	var tc *tuningConn
	cfg := amqp091.Config{
		Heartbeat: testHeartbeat,
		FrameSize: frameSize,
		Dial: func(network, a string) (net.Conn, error) {
			c, err := net.DialTimeout(network, a, 3*time.Second)
			if err != nil {
				return nil, err
			}
			tc = &tuningConn{Conn: c, rewrite: rewrite}
			return tc, nil
		},
	}
	conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/", cfg)
	if tc != nil {
		t.Cleanup(func() { tc.Conn.Close() })
	}
	if err != nil {
		return nil, tc, err
	}
	t.Cleanup(func() { conn.Close() })
	return conn, tc, nil
}

// Heartbeat 0 in tune-ok means "disabled". The broker used to start the
// heartbeat goroutines anyway: time.NewTicker(0) panicked and the connection
// was dropped (#177).
func TestTuneOkHeartbeatZeroDisablesHeartbeats(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)
	conn, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.heartbeat = 0 })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Past the heartbeat monitor's first ticks: nothing may time out.
	time.Sleep(2500 * time.Millisecond)
	roundTrip(t, conn)
	if _, heartbeats := tc.stats(); heartbeats != 0 {
		t.Errorf("broker sent %d heartbeats with heartbeats disabled, want 0", heartbeats)
	}
}

// Heartbeat 1 made the send interval (1>>1) seconds = 0, which panicked the
// same way (#177, #198).
func TestTuneOkHeartbeatOneSendsHeartbeats(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)
	_, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.heartbeat = 1 })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// From here on the client sends nothing, so the only frames on the wire
	// are the broker's heartbeats.
	tc.mute.Store(true)
	t.Cleanup(tc.silence)

	time.Sleep(1500 * time.Millisecond) // the send interval is at least 1 s
	if n := connectionCount(b); n != 1 {
		t.Fatalf("%d connections registered 1.5 s after negotiating heartbeat 1, want 1", n)
	}
	if _, heartbeats := tc.stats(); heartbeats == 0 {
		t.Error("broker sent no heartbeat within 1.5 s of negotiating heartbeat 1")
	}
}
