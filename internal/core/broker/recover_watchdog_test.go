package broker

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoveredPanicIsCounted(t *testing.T) {
	b, _, addr := startTestBroker(t, nil, nil)

	_, victim := dialTap(t, addr, testHeartbeat)
	if _, err := victim.Conn.Write(poisonFrame); err != nil {
		t.Fatalf("write poison frame: %v", err)
	}
	waitPeerClose(t, victim, 3*time.Second)

	if got := b.collector.GetBrokerSnapshot().ConnectionPanicsTotal; got != 1 {
		t.Errorf("ConnectionPanicsTotal = %d, want 1", got)
	}
}

func TestPanicWithBrokerLockHeldExits(t *testing.T) {
	fatal := make(chan string, 1)
	var locked atomic.Bool
	b, _, addr := startTestBroker(t, nil, func(b *Broker, ff *faultyFramer) {
		b.panicCleanupTimeout = 200 * time.Millisecond
		b.fatalAfterPanic = func(msg string) { fatal <- msg }
		// Simulate a handler that panics between b.mu.Lock() and Unlock().
		ff.onPoison = func() { b.mu.Lock(); locked.Store(true) }
	})
	// Release the lock at the end so the stuck cleanup goroutines can finish.
	t.Cleanup(func() {
		if locked.Load() {
			b.mu.Unlock()
		}
	})

	_, victim := dialTap(t, addr, testHeartbeat)
	if _, err := victim.Conn.Write(poisonFrame); err != nil {
		t.Fatalf("write poison frame: %v", err)
	}
	select {
	case msg := <-fatal:
		if !strings.Contains(msg, "lock") {
			t.Errorf("unexpected fatal message: %s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wedged cleanup after a panic did not exit")
	}
}

// Another cleanup of the same connection may win the race, remove it from
// b.Connections and then block on a lock the panic left held. The watchdog
// must still fire.
func TestPanicWatchdogFiresWhenAnotherCleanupIsStuck(t *testing.T) {
	fatal := make(chan string, 1)
	var locked atomic.Bool
	b, _, addr := startTestBroker(t, nil, func(b *Broker, ff *faultyFramer) {
		b.panicCleanupTimeout = 200 * time.Millisecond
		b.fatalAfterPanic = func(msg string) { fatal <- msg }
		ff.onPoison = func() {
			// The panicking handler holds connectionsMu...
			b.connectionsMu.Lock()
			locked.Store(true)
			// ...and a concurrent cleanup has already taken the connection.
			b.mu.Lock()
			var conns []net.Conn
			for c := range b.Connections {
				conns = append(conns, c)
			}
			b.mu.Unlock()
			for _, c := range conns {
				go b.cleanupConnection(c)
			}
			for {
				b.mu.Lock()
				n := len(b.Connections)
				b.mu.Unlock()
				if n == 0 {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	})
	t.Cleanup(func() {
		if locked.Load() {
			b.connectionsMu.Unlock()
		}
	})

	_, victim := dialTap(t, addr, testHeartbeat)
	if _, err := victim.Conn.Write(poisonFrame); err != nil {
		t.Fatalf("write poison frame: %v", err)
	}
	select {
	case <-fatal:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire while another cleanup was stuck on a held lock")
	}
}
