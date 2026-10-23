package broker

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/broker/vhost"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
)

// Regression test for #324: basic.get on another connection's exclusive
// queue closes the channel with 405 RESOURCE_LOCKED and leaves the message in
// the queue; the owner gets it.
func TestBasicGetHandler_ExclusiveQueue(t *testing.T) {
	b := &Broker{framer: &amqp.DefaultFramer{}, connInfos: make(map[net.Conn]*amqp.ConnectionInfo)}
	vh := vhost.NewVhost("/", vhost.VHostOptions{QueueBufferSize: 10, Persistence: &dummy.DummyPersistence{}})
	vh.SetFramer(b.framer)

	ownerID, foreignID := newTestConsumerConnID(), newTestConsumerConnID()
	owner, foreign := &mockConn{}, &mockConn{}
	b.connToID = map[net.Conn]vhost.ConnectionID{owner: ownerID, foreign: foreignID}
	for _, c := range []net.Conn{owner, foreign} {
		b.connInfos[c] = &amqp.ConnectionInfo{Channels: map[uint16]*amqp.ChannelState{1: {}}}
	}

	q, err := vh.CreateQueue("excl", &vhost.QueueProperties{Exclusive: true}, ownerID)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	q.Push(vhost.Message{ID: "m1", Body: []byte("x")})

	req := &amqp.RequestMethodMessage{
		Channel:  1,
		ClassID:  uint16(amqp.BASIC),
		MethodID: uint16(amqp.BASIC_GET),
		Content:  &amqp.BasicGetMessageContent{Queue: "excl", NoAck: true},
	}

	if _, err := b.basicGetHandler(req, vh, foreign); err != nil {
		t.Fatalf("basicGetHandler (foreign): %v", err)
	}
	if !b.connInfos[foreign].Channels[1].ClosingChannel {
		t.Fatal("foreign channel not closed")
	}
	// method frame: type, channel(2), size(4), class(2), method(2), reply-code(2)
	w := foreign.written
	if len(w) < 13 || binary.BigEndian.Uint16(w[7:9]) != uint16(amqp.CHANNEL) || binary.BigEndian.Uint16(w[9:11]) != uint16(amqp.CHANNEL_CLOSE) {
		t.Fatalf("foreign connection got %x, want channel.close", w)
	}
	if code := binary.BigEndian.Uint16(w[11:13]); code != uint16(amqp.RESOURCE_LOCKED) {
		t.Fatalf("channel.close reply code %d, want %d", code, amqp.RESOURCE_LOCKED)
	}
	if q.Len() != 1 {
		t.Fatalf("queue holds %d messages after the refused get, want 1", q.Len())
	}

	if _, err := b.basicGetHandler(req, vh, owner); err != nil {
		t.Fatalf("basicGetHandler (owner): %v", err)
	}
	if b.connInfos[owner].Channels[1].ClosingChannel {
		t.Fatal("owner's channel was closed")
	}
	if q.Len() != 0 {
		t.Fatalf("owner's get left %d messages, want 0", q.Len())
	}
}
