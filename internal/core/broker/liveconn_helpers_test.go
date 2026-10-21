package broker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ottermq/ottermq/config"
	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/persistdb"
	"github.com/ottermq/ottermq/pkg/metrics"
	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

// Helpers for tests that run a real broker on a loopback port and talk to it
// over TCP.

// testLogs receives a copy of everything the broker logs during the tests.
var testLogs lockedBuffer

func TestMain(m *testing.M) {
	// Set once, before any broker goroutine runs, so tests can inspect logs
	// without racing on the global logger.
	log.Logger = log.Logger.Output(io.MultiWriter(os.Stderr, &testLogs))
	os.Exit(m.Run())
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var testDBOnce sync.Once

// setupTestDB opens the user database once per test binary and adds
// guest/guest. The password hash uses bcrypt.MinCost: the default cost takes
// seconds per login under -race.
func setupTestDB(t *testing.T) {
	t.Helper()
	testDBOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ottermq-broker-test-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		persistdb.SetDbPath(filepath.Join(dir, "test.db"))
		if err := persistdb.OpenDB(); err != nil {
			t.Fatalf("open db: %v", err)
		}
		persistdb.InitDB()
		persistdb.AddDefaultRoles()
		persistdb.AddDefaultPermissions()
		hash, err := bcrypt.GenerateFromPassword([]byte("guest"), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := persistdb.AddUserWithHash("guest", string(hash), 1); err != nil {
			t.Fatalf("add user: %v", err)
		}
		if err := persistdb.GrantVHostAccess("guest", "/"); err != nil {
			t.Fatalf("grant vhost: %v", err)
		}
	})
}

// faultyFramer wraps the real framer and injects panics on demand.
type faultyFramer struct {
	amqp.Framer
	panicNextHandshake atomic.Bool
	// onPoison, if set, runs right before ParseFrame panics on a poison frame.
	onPoison func()
	// afterHandshake, if set, runs after a real handshake completes.
	afterHandshake func()
}

// poisonChannel marks frames that make faultyFramer.ParseFrame panic. Real
// clients never use it.
const poisonChannel = 0xBEEF

// poisonFrame is a heartbeat frame on poisonChannel.
var poisonFrame = []byte{byte(amqp.TYPE_HEARTBEAT), 0xBE, 0xEF, 0, 0, 0, 0, amqp.FRAME_END}

func (f *faultyFramer) Handshake(configurations *map[string]any, conn net.Conn, connCtx context.Context) (*amqp.ConnectionInfo, error) {
	if f.panicNextHandshake.CompareAndSwap(true, false) {
		panic("injected handshake panic")
	}
	info, err := f.Framer.Handshake(configurations, conn, connCtx)
	if f.afterHandshake != nil {
		f.afterHandshake()
	}
	return info, err
}

func (f *faultyFramer) ParseFrame(frame []byte) (any, error) {
	if len(frame) >= 3 && binary.BigEndian.Uint16(frame[1:3]) == poisonChannel {
		if f.onPoison != nil {
			f.onPoison()
		}
		panic("injected frame panic")
	}
	return f.Framer.ParseFrame(frame)
}

// startTestBroker starts a broker on a random loopback port and returns it
// with its AMQP address. The broker's framer is a faultyFramer. modify edits
// the config before the broker is created; setup runs before Start, which is
// where test hooks must be set to be race-free.
func startTestBroker(t *testing.T, modify func(*config.Config), setup func(*Broker, *faultyFramer)) (*Broker, *faultyFramer, string) {
	t.Helper()
	setupTestDB(t)

	cfg := &config.Config{
		BrokerHost:           "127.0.0.1",
		BrokerPort:           "0",
		DataDir:              t.TempDir(),
		Version:              "test",
		QueueBufferSize:      1000,
		MaxPriority:          10,
		HeartbeatIntervalMax: 60,
		ChannelMax:           2048,
		FrameMax:             131072,
	}
	if modify != nil {
		modify(cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	collector := metrics.NewCollector(metrics.DefaultConfig(), ctx)
	b := NewBroker(cfg, ctx, cancel, collector)
	user, err := persistdb.GetUserByUsername("guest")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	b.VHosts[DEFAULT_VHOST].Users[user.Username] = &user

	ff := &faultyFramer{Framer: b.framer}
	b.framer = ff
	if setup != nil {
		setup(b, ff)
	}

	go func() { _ = b.Start() }()
	select {
	case <-b.Ready:
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not start")
	}
	addr := b.listener.Addr().String()

	t.Cleanup(func() {
		cancel()
		b.listener.Close()
		b.mu.Lock()
		for conn := range b.Connections {
			conn.Close()
		}
		b.mu.Unlock()
	})
	return b, ff, addr
}

// tapConn is the client side of an amqp091 connection. It lets a test write
// raw bytes past the client library, silence the client, and see how the
// socket ended. Close is a no-op, so that when amqp091 gives up on the
// connection the socket stays open and the test can still observe whether the
// broker closes it; the real socket is closed at test cleanup.
type tapConn struct {
	net.Conn
	mute    atomic.Bool
	readErr chan error
}

func (c *tapConn) Write(p []byte) (int, error) {
	if c.mute.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *tapConn) Close() error { return nil }

func (c *tapConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		select {
		case c.readErr <- err:
		default:
		}
	}
	return n, err
}

// testHeartbeat is amqp091's usual default.
const testHeartbeat = 10 * time.Second

// dialTap opens an amqp091 connection and returns it with its tapConn.
func dialTap(t *testing.T, addr string, heartbeat time.Duration) (*amqp091.Connection, *tapConn) {
	t.Helper()
	var tc *tapConn
	cfg := amqp091.Config{
		Heartbeat: heartbeat,
		Dial: func(network, a string) (net.Conn, error) {
			c, err := net.DialTimeout(network, a, 3*time.Second)
			if err != nil {
				return nil, err
			}
			if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				return nil, err
			}
			tc = &tapConn{Conn: c, readErr: make(chan error, 1)}
			return tc, nil
		},
	}
	conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/", cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		tc.Conn.Close()
	})
	return conn, tc
}

// isClosedByPeer reports whether a read error means the peer closed the
// socket (EOF or RST), as opposed to a timeout or a local close.
func isClosedByPeer(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}

// waitPeerClose waits until the server closes the tapped socket.
func waitPeerClose(t *testing.T, tc *tapConn, within time.Duration) {
	t.Helper()
	select {
	case err := <-tc.readErr:
		if !isClosedByPeer(err) {
			t.Fatalf("socket ended with %v, want EOF/RST from the broker", err)
		}
	case <-time.After(within):
		t.Fatalf("broker did not close the socket within %s", within)
	}
}

// readUntilClosed reads from a raw conn until it ends or the deadline passes,
// and returns the error that ended it.
func readUntilClosed(conn net.Conn, within time.Duration) error {
	_ = conn.SetReadDeadline(time.Now().Add(within))
	_, err := io.Copy(io.Discard, conn)
	if err == nil {
		return io.EOF // io.Copy reports EOF as nil
	}
	return err
}

// roundTrip publishes one message on conn and checks that it is consumed.
func roundTrip(t *testing.T, conn *amqp091.Connection) {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer ch.Close()
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("queue.declare: %v", err)
	}
	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	body := []byte("still alive")
	if err := ch.Publish("", q.Name, false, false, amqp091.Publishing{Body: body}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case m := <-msgs:
		if !bytes.Equal(m.Body, body) {
			t.Fatalf("got body %q, want %q", m.Body, body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message not delivered")
	}
}
