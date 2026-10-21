package broker

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/config"
	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/broker/vhost"
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

// wantCloseCode waits for the broker to close conn with connection.close code.
func wantCloseCode(t *testing.T, closed <-chan *amqp091.Error, code int) {
	t.Helper()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatalf("connection closed without an error, want connection.close %d", code)
		}
		if err.Code != code {
			t.Fatalf("connection closed with %d (%s), want %d", err.Code, err.Reason, code)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("broker did not send connection.close %d", code)
	}
}

// methodFrame builds a method frame with the given payload after class/method.
func methodFrame(channel uint16, classID, methodID uint16, args []byte) []byte {
	payload := binary.BigEndian.AppendUint16(nil, classID)
	payload = binary.BigEndian.AppendUint16(payload, methodID)
	payload = append(payload, args...)
	frame := []byte{byte(amqp.TYPE_METHOD)}
	frame = binary.BigEndian.AppendUint16(frame, channel)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	return append(frame, amqp.FRAME_END)
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

// The client's heartbeat is capped at the configured maximum (#198).
func TestTuneOkHeartbeatIsCappedAtServerMax(t *testing.T) {
	b, _, addr := startTestBroker(t, func(c *config.Config) { c.HeartbeatIntervalMax = 2 }, nil)
	_, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.heartbeat = 600 })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// amqp091 itself picked 2 s and would send heartbeats; silence it so the
	// only frames on the wire are the broker's.
	tc.mute.Store(true)
	t.Cleanup(tc.silence)

	// Capped at 2 s, the broker sends a heartbeat every second; with 600 s
	// it would send one every 300 s.
	time.Sleep(1500 * time.Millisecond)
	if n := connectionCount(b); n != 1 {
		t.Fatalf("%d connections registered after 1.5 s, want 1", n)
	}
	if _, heartbeats := tc.stats(); heartbeats == 0 {
		t.Error("broker sent no heartbeat within 1.5 s; tune-ok heartbeat 600 was not capped at 2")
	}
}

// frame-max below the spec minimum (4096) or above the server's offer is
// refused with connection.close 530 (#198).
func TestTuneOkFrameMaxOutOfRangeIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name     string
		frameMax uint32
	}{
		{"below 4096", 1024},
		{"above server frame-max", 1 << 20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, addr := startTestBroker(t, func(c *config.Config) { c.FrameMax = 8192 }, nil)
			_, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.frameMax = tt.frameMax })
			if err == nil {
				t.Fatal("dial succeeded, want the broker to refuse the tune-ok")
			}
			// amqp091 reports any failure during connection.open as 403, so
			// read the reply code off the wire.
			if codes := tc.receivedCloseCodes(); len(codes) != 1 || codes[0] != uint16(amqp.NOT_ALLOWED) {
				t.Fatalf("connection.close reply codes %v, want [530]", codes)
			}
		})
	}
}

// channel.open on channel 0 or above the negotiated channel-max is refused
// with connection.close 530 (#198). tune-ok channel-max 0 means the server's
// value, not "unlimited".
func TestChannelOpenOutsideChannelMaxIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name          string
		tuneOkChanMax uint16
		channel       uint16
	}{
		{"above channel-max", 10, 11},
		{"tune-ok 0 means server value", 0, 11},
		{"channel 0", 10, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, addr := startTestBroker(t, func(c *config.Config) { c.ChannelMax = 10 }, nil)
			conn, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.channelMax = tt.tuneOkChanMax })
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			closed := conn.NotifyClose(make(chan *amqp091.Error, 1))
			// Past amqp091, which never opens a channel above channel-max.
			if _, err := tc.Conn.Write(methodFrame(tt.channel, uint16(amqp.CHANNEL), uint16(amqp.CHANNEL_OPEN), []byte{0})); err != nil {
				t.Fatalf("write channel.open: %v", err)
			}
			wantCloseCode(t, closed, amqp091.NotAllowed)
		})
	}

	t.Run("at channel-max", func(t *testing.T) {
		_, _, addr := startTestBroker(t, func(c *config.Config) { c.ChannelMax = 10 }, nil)
		conn, _, err := dialTuning(t, addr, 0, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		for i := 0; i < 10; i++ { // amqp091 allocates ids 1..10
			if _, err := conn.Channel(); err != nil {
				t.Fatalf("channel %d: %v", i+1, err)
			}
		}
	})
}

// A frame header declaring more than frame-max is refused with
// connection.close 501 before the payload is read or allocated (#179). The
// declared size is 2 x frame-max, so the run before the fix stays harmless.
func TestFrameAboveFrameMaxIsRefused(t *testing.T) {
	const frameMax = 4096
	_, _, addr := startTestBroker(t, func(c *config.Config) { c.FrameMax = frameMax }, nil)
	conn, tc, err := dialTuning(t, addr, 0, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closed := conn.NotifyClose(make(chan *amqp091.Error, 1))

	header := []byte{byte(amqp.TYPE_BODY), 0, 1}
	header = binary.BigEndian.AppendUint32(header, 2*frameMax)
	if _, err := tc.Conn.Write(header); err != nil {
		t.Fatalf("write frame header: %v", err)
	}
	wantCloseCode(t, closed, amqp091.FrameError)
}

// The same limit applies during the handshake, before tune-ok, using the
// server's configured frame-max (#179).
func TestHandshakeFrameAboveFrameMaxIsRefused(t *testing.T) {
	const frameMax = 4096
	_, _, addr := startTestBroker(t, func(c *config.Config) {
		c.FrameMax = frameMax
		c.HandshakeTimeout = 5 * time.Second
	}, nil)

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("AMQP\x00\x00\x09\x01")); err != nil {
		t.Fatalf("write header: %v", err)
	}
	// In place of start-ok: a frame header declaring 2 x frame-max.
	header := []byte{byte(amqp.TYPE_METHOD), 0, 0}
	header = binary.BigEndian.AppendUint32(header, 2*frameMax)
	if _, err := raw.Write(header); err != nil {
		t.Fatalf("write frame header: %v", err)
	}

	// Well inside the handshake timeout, the broker sends connection.start,
	// then connection.close 501, and closes the socket.
	_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(raw)
	if err != nil {
		t.Fatalf("socket ended with %v, want EOF from the broker after connection.close", err)
	}
	var replyCodes []uint16
	for len(got) >= 8 {
		size := int(binary.BigEndian.Uint32(got[3:7]))
		if len(got) < 7+size+1 {
			break
		}
		payload := got[7 : 7+size]
		if got[0] == byte(amqp.TYPE_METHOD) && size >= 6 &&
			binary.BigEndian.Uint16(payload[0:2]) == uint16(amqp.CONNECTION) &&
			binary.BigEndian.Uint16(payload[2:4]) == uint16(amqp.CONNECTION_CLOSE) {
			replyCodes = append(replyCodes, binary.BigEndian.Uint16(payload[4:6]))
		}
		got = got[7+size+1:]
	}
	if len(replyCodes) != 1 || replyCodes[0] != uint16(amqp.FRAME_ERROR) {
		t.Fatalf("connection.close reply codes %v, want [501]", replyCodes)
	}
}

// Outgoing bodies are split into body frames of at most frame-max octets.
// The broker used to send each body as one frame, whatever its size (#198).
// The 3 x frame-max message is published through the vhost: amqp091 would
// split it across body frames, which the broker cannot reassemble yet (#221).
func TestOutgoingBodyIsSplitAtFrameMax(t *testing.T) {
	const frameMax = 4096
	body := bytes.Repeat([]byte("0123456789abcdef"), 3*frameMax/16) // 3 x frame-max

	// setup returns an amqp091 channel on a connection that negotiated
	// frameMax, the name of a queue on it, and a func that publishes body there.
	setup := func(t *testing.T) (*amqp091.Channel, *tuningConn, string, func()) {
		t.Helper()
		b, _, addr := startTestBroker(t, func(c *config.Config) { c.FrameMax = frameMax }, nil)
		// tune-ok frame-max 0 means the server's value.
		conn, tc, err := dialTuning(t, addr, 0, func(o *tuneOk) { o.frameMax = 0 })
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		ch, err := conn.Channel()
		if err != nil {
			t.Fatalf("channel: %v", err)
		}
		q, err := ch.QueueDeclare("", false, true, false, false, nil)
		if err != nil {
			t.Fatalf("queue.declare: %v", err)
		}
		publish := func() {
			msg := &vhost.Message{ID: "big", Body: body, RoutingKey: q.Name}
			if _, err := b.GetVHost(DEFAULT_VHOST).Publish("", q.Name, msg); err != nil {
				t.Fatalf("publish: %v", err)
			}
		}
		return ch, tc, q.Name, publish
	}
	checkFrames := func(t *testing.T, tc *tuningConn, got []byte) {
		t.Helper()
		if !bytes.Equal(got, body) {
			t.Fatalf("got a %d-byte body, want the %d bytes sent", len(got), len(body))
		}
		if maxBody, _ := tc.stats(); maxBody > frameMax {
			t.Errorf("largest body frame is %d octets, want at most frame-max %d", maxBody, frameMax)
		}
	}

	t.Run("basic.deliver", func(t *testing.T) {
		ch, tc, q, publish := setup(t)
		msgs, err := ch.Consume(q, "", true, false, false, false, nil)
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		publish()
		select {
		case m := <-msgs:
			checkFrames(t, tc, m.Body)
		case <-time.After(3 * time.Second):
			t.Fatal("message not delivered")
		}
	})

	t.Run("basic.get-ok", func(t *testing.T) {
		ch, tc, q, publish := setup(t)
		publish()
		deadline := time.Now().Add(3 * time.Second)
		for {
			m, ok, err := ch.Get(q, true)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if ok {
				checkFrames(t, tc, m.Body)
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("basic.get found no message")
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}
