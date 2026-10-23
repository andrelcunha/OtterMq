package vhost

import (
	"strings"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
	amqperr "github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/ottermq/ottermq/pkg/persistence/implementations/dummy"
)

// newExclusiveQueueVhost returns a vhost with an exclusive queue "excl" owned
// by the returned connection ID.
func newExclusiveQueueVhost(t *testing.T) (*VHost, ConnectionID) {
	t.Helper()
	vh := NewVhost("/", VHostOptions{QueueBufferSize: 10, Persistence: &dummy.DummyPersistence{}})
	owner := newTestConsumerConnID()
	if _, err := vh.CreateQueue("excl", &QueueProperties{Exclusive: true}, owner); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	return vh, owner
}

// assertResourceLocked checks err is a 405 RESOURCE_LOCKED channel exception
// for the given method, worded like RabbitMQ's.
func assertResourceLocked(t *testing.T, err error, classID, methodID uint16) {
	t.Helper()
	if err == nil {
		t.Fatal("expected 405 RESOURCE_LOCKED, got no error")
	}
	amqpErr, ok := err.(amqperr.AMQPError)
	if !ok {
		t.Fatalf("expected AMQPError, got %T (%v)", err, err)
	}
	if amqpErr.ReplyCode() != uint16(amqp.RESOURCE_LOCKED) {
		t.Errorf("reply code %d (%s), want %d RESOURCE_LOCKED", amqpErr.ReplyCode(), amqpErr.ReplyText(), amqp.RESOURCE_LOCKED)
	}
	if amqpErr.ClassID() != classID || amqpErr.MethodID() != methodID {
		t.Errorf("class/method %d/%d, want %d/%d", amqpErr.ClassID(), amqpErr.MethodID(), classID, methodID)
	}
	want := "RESOURCE_LOCKED - cannot obtain exclusive access to locked queue 'excl' in vhost '/'"
	if !strings.HasPrefix(amqpErr.ReplyText(), want) {
		t.Errorf("reply text %q, want it to start with %q", amqpErr.ReplyText(), want)
	}
}

// queue.bind and queue.unbind refuse another connection with 405 for their
// own method (bind used to report queue.unbind).
func TestBindQueue_ExclusiveQueue(t *testing.T) {
	vh, owner := newExclusiveQueueVhost(t)
	if err := vh.CreateExchange("ex", DIRECT, &ExchangeProperties{}); err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}

	err := vh.BindQueue("ex", "excl", "k", nil, newTestConsumerConnID())
	assertResourceLocked(t, err, uint16(amqp.QUEUE), uint16(amqp.QUEUE_BIND))
	if err := vh.BindQueue("ex", "excl", "k", nil, owner); err != nil {
		t.Fatalf("owner's queue.bind: %v", err)
	}

	err = vh.UnbindQueue("ex", "excl", "k", nil, newTestConsumerConnID())
	assertResourceLocked(t, err, uint16(amqp.QUEUE), uint16(amqp.QUEUE_UNBIND))
	if err := vh.UnbindQueue("ex", "excl", "k", nil, owner); err != nil {
		t.Fatalf("owner's queue.unbind: %v", err)
	}
}
