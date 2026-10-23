package broker

import (
	"net"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/broker/vhost"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

// txCommitFixture opens a transactional channel on a live broker, buffers n
// publishes to a queue, and returns what a test needs to commit them while
// cleaning up. The client socket is closed at cleanup without waiting for the
// broker, so a deadlocked broker can't hang the test.
func txCommitFixture(t *testing.T, n int) (b *Broker, ch *amqp091.Channel, vh *vhost.VHost, connID vhost.ConnectionID, queue *vhost.Queue) {
	t.Helper()
	b, _, addr := startTestBroker(t, nil, nil)
	var raw net.Conn
	conn, err := amqp091.DialConfig("amqp://guest:guest@"+addr+"/", amqp091.Config{
		Dial: func(network, a string) (net.Conn, error) {
			c, err := net.DialTimeout(network, a, 3*time.Second)
			raw = c
			return c, err
		},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { raw.Close() })

	ch, err = conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := ch.QueueDeclare("tx-cleanup", false, false, false, false, nil); err != nil {
		t.Fatalf("queue.declare: %v", err)
	}
	if err := ch.Tx(); err != nil {
		t.Fatalf("tx.select: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := ch.Publish("", "tx-cleanup", false, false, amqp091.Publishing{Body: []byte("m")}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	vh = b.GetVHost(DEFAULT_VHOST)
	b.connectionsMu.Lock()
	for id := range b.connections {
		connID = id
	}
	b.connectionsMu.Unlock()
	queue = vh.GetQueue("tx-cleanup")

	// Wait until every publish is buffered, so the commit has n to apply.
	deadline := time.Now().Add(30 * time.Second)
	for {
		txState := vh.GetTransactionState(ch1, connID)
		if txState != nil {
			txState.Lock()
			buffered := len(txState.BufferedPublishes)
			txState.Unlock()
			if buffered == n {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("publishes were not buffered within 30s")
		}
		time.Sleep(time.Millisecond)
	}
	return b, ch, vh, connID, queue
}

// ch1 is the channel amqp091 opens first.
const ch1 = 1

// startCommit runs tx.commit in the background and waits until the broker
// is applying it (the queue starts filling), so whatever the test does next
// overlaps the commit.
func startCommit(t *testing.T, ch *amqp091.Channel, queue *vhost.Queue) <-chan error {
	t.Helper()
	committed := make(chan error, 1)
	go func() { committed <- ch.TxCommit() }()
	deadline := time.Now().Add(5 * time.Second)
	for queue.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("commit did not start applying publishes within 5s")
		}
		time.Sleep(50 * time.Microsecond)
	}
	return committed
}

// The path #311 names: the connection is torn down (management close,
// heartbeat timeout) while its channel commits. cleanupConnection runs
// vh.CleanupConnection, which doesn't touch the transaction state.
func TestTxCommit_ConnectionCleanupDuringCommit(t *testing.T) {
	b, ch, vh, connID, queue := txCommitFixture(t, 2000)

	committed := startCommit(t, ch, queue)

	cleaned := make(chan struct{})
	go func() {
		defer close(cleaned)
		b.connectionsMu.Lock()
		conn := b.connections[connID]
		b.connectionsMu.Unlock()
		b.cleanupConnection(conn)
	}()
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanupConnection did not return within 5s while the channel was committing")
	}
	select {
	case <-committed: // an error is fine: the connection is gone
	case <-time.After(5 * time.Second):
	}
	if !finishesWithin(func() { vh.GetQueue("tx-cleanup") }, 2*time.Second) {
		t.Fatal("vh.mu is still held 2s after the cleanup")
	}
}

// CleanupChannel during a commit of the same channel. CleanupChannel has no
// production caller yet (channel.close doesn't clean up the vhost, #219), but
// it takes the transaction lock under vh.mu while the commit takes vh.mu
// under the transaction lock.
func TestTxCommit_CleanupChannelDuringCommit(t *testing.T) {
	_, ch, vh, connID, queue := txCommitFixture(t, 2000)
	committed := startCommit(t, ch, queue)

	if !finishesWithin(func() { vh.CleanupChannel(connID, ch1) }, 5*time.Second) {
		t.Fatal("CleanupChannel did not return within 5s while the channel was committing")
	}
	select {
	case <-committed:
	case <-time.After(5 * time.Second):
		t.Fatal("tx.commit did not finish within 5s after CleanupChannel")
	}
	if !finishesWithin(func() { vh.GetQueue("tx-cleanup") }, 2*time.Second) {
		t.Fatal("vh.mu is still held 2s after the cleanup")
	}
}

func finishesWithin(f func(), d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
