package vhost

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type sentCancel struct {
	connID  ConnectionID
	channel uint16
	tag     string
}

// cancelNotifySender is a FrameSender whose connections advertise
// consumer_cancel_notify or not. It records the basic.cancel frames it is
// sent (MockFramer renders them as "basic-cancel:<tag>") and whether one was
// sent while vh.mu was held.
type cancelNotifySender struct {
	vh     *VHost
	notify map[ConnectionID]bool
	// block, if set, holds every write to that connection until closed, as
	// a client that stopped reading would.
	block     ConnectionID
	unblocked chan struct{}

	mu        sync.Mutex
	cancels   []sentCancel
	underLock bool
}

func (s *cancelNotifySender) ConsumerCancelNotify(connID ConnectionID) bool {
	return s.notify[connID]
}

func (s *cancelNotifySender) SendFrame(connID ConnectionID, channel uint16, frame []byte) error {
	tag, ok := strings.CutPrefix(string(frame), "basic-cancel:")
	if !ok {
		return nil
	}
	if s.unblocked != nil && connID == s.block {
		<-s.unblocked
	}
	// If the caller holds vh.mu, this can't take it until the caller is done.
	locked := make(chan struct{})
	go func() {
		s.vh.mu.Lock()
		s.vh.mu.Unlock()
		close(locked)
	}()
	held := false
	select {
	case <-locked:
	case <-time.After(500 * time.Millisecond):
		held = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels = append(s.cancels, sentCancel{connID, channel, tag})
	s.underLock = s.underLock || held
	return nil
}

func (s *cancelNotifySender) sent() ([]sentCancel, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentCancel(nil), s.cancels...), s.underLock
}

func cancelTestQueueProps() *QueueProperties {
	return &QueueProperties{Arguments: map[string]any{}}
}

func queueExists(vh *VHost, name string) bool {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	_, ok := vh.Queues[name]
	return ok
}

func newCancelNotifyVHost(t *testing.T, notify map[ConnectionID]bool) (*VHost, *cancelNotifySender) {
	t.Helper()
	sender := &cancelNotifySender{notify: notify}
	vh := newLoopTestVHost(t, sender)
	sender.vh = vh
	return vh, sender
}

func registerNoAck(t *testing.T, vh *VHost, connID ConnectionID, channel uint16, queue, tag string) {
	t.Helper()
	if _, err := vh.RegisterConsumer(NewConsumer(connID, channel, queue, tag, &ConsumerProperties{NoAck: true})); err != nil {
		t.Fatalf("RegisterConsumer %s: %v", tag, err)
	}
}

// Deleting a queue sends basic.cancel, after vh.mu is released, to each of
// its consumers whose client advertised consumer_cancel_notify, and to no
// one else (#320).
func TestDeleteQueueSendsBasicCancelToNotifyingClients(t *testing.T) {
	notifying, silent := newTestConsumerConnID(), newTestConsumerConnID()
	vh, sender := newCancelNotifyVHost(t, map[ConnectionID]bool{notifying: true})

	for _, name := range []string{"q", "other"} {
		if _, err := vh.CreateQueue(name, cancelTestQueueProps(), notifying); err != nil {
			t.Fatalf("CreateQueue %s: %v", name, err)
		}
	}
	t.Cleanup(func() { deleteWithin(t, vh, "other", time.Second) })
	registerNoAck(t, vh, notifying, 1, "q", "a")
	registerNoAck(t, vh, silent, 2, "q", "b")
	registerNoAck(t, vh, notifying, 3, "other", "c")

	if err := vh.DeleteQueuebyName("q"); err != nil {
		t.Fatalf("DeleteQueuebyName: %v", err)
	}

	// Written before DeleteQueuebyName returns, so before queue.delete-ok.
	cancels, underLock := sender.sent()
	want := sentCancel{notifying, 1, "a"}
	if len(cancels) != 1 || cancels[0] != want {
		t.Errorf("basic.cancel sent = %+v, want only %+v", cancels, want)
	}
	if underLock {
		t.Error("basic.cancel was written while vh.mu was held")
	}
}

// Without the capability, a removed consumer's client gets no basic.cancel.
func TestDeleteQueueSendsNoBasicCancelWithoutCapability(t *testing.T) {
	connID := newTestConsumerConnID()
	vh, sender := newCancelNotifyVHost(t, map[ConnectionID]bool{})

	if _, err := vh.CreateQueue("q", cancelTestQueueProps(), connID); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	registerNoAck(t, vh, connID, 1, "q", "a")
	if err := vh.DeleteQueuebyName("q"); err != nil {
		t.Fatalf("DeleteQueuebyName: %v", err)
	}
	if cancels, _ := sender.sent(); len(cancels) != 0 {
		t.Errorf("basic.cancel sent = %+v, want none", cancels)
	}
}

// When a closing connection's exclusive queue is deleted, its own consumers
// aren't notified (the connection is going away). Another connection can't
// consume from the queue (405 RESOURCE_LOCKED, #324), so nobody is notified.
func TestCleanupConnectionDoesNotNotifyOwner(t *testing.T) {
	owner, other := newTestConsumerConnID(), newTestConsumerConnID()
	vh, sender := newCancelNotifyVHost(t, map[ConnectionID]bool{owner: true, other: true})

	props := cancelTestQueueProps()
	props.Exclusive = true
	if _, err := vh.CreateQueue("excl", props, owner); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	registerNoAck(t, vh, owner, 1, "excl", "own")
	foreign := NewConsumer(other, 2, "excl", "foreign", &ConsumerProperties{NoAck: true})
	if _, err := vh.RegisterConsumer(foreign); err == nil {
		t.Fatal("another connection could consume from the exclusive queue")
	}

	vh.CleanupConnection(owner)

	if cancels, _ := sender.sent(); len(cancels) != 0 {
		t.Errorf("basic.cancel sent = %+v, want none", cancels)
	}
	if queueExists(vh, "excl") {
		t.Error("the exclusive queue was not deleted")
	}
}

// Cancelling the last consumer of an auto-delete queue deletes it, with no
// consumer left to notify; the client that cancelled gets only cancel-ok
// (sent by the caller), not a basic.cancel.
func TestAutoDeleteQueueSendsNoBasicCancel(t *testing.T) {
	connID := newTestConsumerConnID()
	vh, sender := newCancelNotifyVHost(t, map[ConnectionID]bool{connID: true})

	props := cancelTestQueueProps()
	props.AutoDelete = true
	if _, err := vh.CreateQueue("auto", props, connID); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	registerNoAck(t, vh, connID, 1, "auto", "a")
	if err := vh.CancelConsumer(1, "a"); err != nil {
		t.Fatalf("CancelConsumer: %v", err)
	}
	if queueExists(vh, "auto") {
		t.Fatal("the auto-delete queue was not deleted")
	}
	if cancels, _ := sender.sent(); len(cancels) != 0 {
		t.Errorf("basic.cancel sent = %+v, want none", cancels)
	}
}

// A client that stopped reading holds up the queue deletion that removed its
// consumer for at most cancelNotifyWait, and the other clients are notified
// before it returns.
func TestDeleteQueueIsNotHeldUpByUnresponsiveClient(t *testing.T) {
	stuck, other := newTestConsumerConnID(), newTestConsumerConnID()
	vh, sender := newCancelNotifyVHost(t, map[ConnectionID]bool{stuck: true, other: true})
	sender.block = stuck
	sender.unblocked = make(chan struct{})
	defer close(sender.unblocked)

	if _, err := vh.CreateQueue("q", cancelTestQueueProps(), stuck); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	registerNoAck(t, vh, stuck, 1, "q", "stuck")
	registerNoAck(t, vh, other, 2, "q", "other")

	deleteWithin(t, vh, "q", 3*cancelNotifyWait)

	cancels, _ := sender.sent()
	want := sentCancel{other, 2, "other"}
	if len(cancels) != 1 || cancels[0] != want {
		t.Errorf("basic.cancel sent = %+v, want %+v while the stuck client blocks", cancels, want)
	}
}

// orderSender records the kind of each frame it is sent, in order, and holds
// the first delivery until gate is closed.
type orderSender struct {
	gate    chan struct{}
	blocked chan struct{}
	once    sync.Once

	mu     sync.Mutex
	frames []string
}

func (s *orderSender) ConsumerCancelNotify(ConnectionID) bool { return true }

func (s *orderSender) SendFrame(_ ConnectionID, _ uint16, frame []byte) error {
	kind := "other"
	switch {
	case strings.HasPrefix(string(frame), "basic-deliver"):
		kind = "deliver"
		s.once.Do(func() {
			close(s.blocked)
			<-s.gate
		})
	case strings.HasPrefix(string(frame), "basic-cancel:"):
		kind = "cancel"
	}
	s.mu.Lock()
	s.frames = append(s.frames, kind)
	s.mu.Unlock()
	return nil
}

func (s *orderSender) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.frames...)
}

// A delivery already being written when its queue is deleted reaches the
// client before the basic.cancel for its consumer: a client drops (or hands
// to a consumer reusing the tag) a delivery that arrives after the cancel.
func TestBasicCancelDoesNotOvertakeInFlightDelivery(t *testing.T) {
	sender := &orderSender{gate: make(chan struct{}), blocked: make(chan struct{})}
	vh := newLoopTestVHost(t, sender)
	connID := newTestConsumerConnID()

	q, err := vh.CreateQueue("q", cancelTestQueueProps(), connID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	registerNoAck(t, vh, connID, 1, "q", "c")
	q.Push(Message{ID: "m1"})
	select {
	case <-sender.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("the delivery did not start")
	}

	deleted := make(chan error, 1)
	go func() { deleted <- vh.DeleteQueuebyName("q") }()
	time.Sleep(100 * time.Millisecond) // a cancel not waiting for the delivery would be written by now
	close(sender.gate)
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("DeleteQueuebyName: %v", err)
		}
	case <-time.After(3 * cancelNotifyWait):
		t.Fatal("DeleteQueuebyName did not return")
	}

	if !waitFor(2*time.Second, func() bool { return len(sender.kinds()) >= 2 }) {
		t.Fatalf("frames sent = %v, want a delivery and a cancel", sender.kinds())
	}
	if got := sender.kinds(); got[0] != "deliver" || got[1] != "cancel" {
		t.Errorf("frames sent = %v, want the delivery before the cancel", got)
	}
}
