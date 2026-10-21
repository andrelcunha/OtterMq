package vhost

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/testutil"
)

// gateSender is a FrameSender that records which channels received a
// delivery. While gate is non-nil, the first delivery blocks until gate is
// closed, holding the delivery loop inside deliverToConsumer.
type gateSender struct {
	gate    chan struct{}
	blocked chan struct{} // closed when the first delivery starts blocking
	once    sync.Once

	mu         sync.Mutex
	byChannel  map[uint16]int
	deliveries atomic.Int64
}

func newGateSender(gated bool) *gateSender {
	s := &gateSender{byChannel: make(map[uint16]int), blocked: make(chan struct{})}
	if gated {
		s.gate = make(chan struct{})
	}
	return s
}

func (s *gateSender) SendFrame(_ ConnectionID, channel uint16, _ []byte) error {
	if s.gate != nil {
		s.once.Do(func() {
			close(s.blocked)
			<-s.gate
		})
	}
	s.mu.Lock()
	s.byChannel[channel]++
	s.mu.Unlock()
	s.deliveries.Add(1)
	return nil
}

func (s *gateSender) deliveredOn(channel uint16) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byChannel[channel]
}

func newLoopTestVHost(t *testing.T, sender FrameSender) *VHost {
	t.Helper()
	vh := setupTestVHost()
	vh.framer = &testutil.MockFramer{}
	vh.SetFrameSender(sender)
	return vh
}

// waitFor polls cond until it holds or timeout passes.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// failOnChannelSender fails every delivery to one channel, as a consumer
// whose connection broke would, and records the others.
type failOnChannelSender struct {
	failChannel uint16
	gateSender
}

func newFailOnChannelSender(channel uint16) *failOnChannelSender {
	return &failOnChannelSender{
		failChannel: channel,
		gateSender:  gateSender{byChannel: make(map[uint16]int), blocked: make(chan struct{})},
	}
}

func (s *failOnChannelSender) SendFrame(connID ConnectionID, channel uint16, frame []byte) error {
	if channel == s.failChannel {
		if s.gate != nil { // hold the first failing write until gate closes
			s.once.Do(func() {
				close(s.blocked)
				<-s.gate
			})
		}
		return errors.New("injected write failure")
	}
	return s.gateSender.SendFrame(connID, channel, frame)
}
