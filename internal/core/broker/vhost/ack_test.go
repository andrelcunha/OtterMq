package vhost

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ottermq/ottermq/internal/core/amqp"
	amqperr "github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/ottermq/ottermq/pkg/metrics"
)

// helper to build a minimal consumer
func newTestConsumer(connID ConnectionID, ch uint16, queue string, noAck bool) *Consumer {
	c := &Consumer{
		Tag:          "ctag-test",
		Channel:      ch,
		QueueName:    queue,
		ConnectionID: connID,
		Props: &ConsumerProperties{
			NoAck: noAck,
		},
	}
	c.Active.Store(true)
	return c
}

func newTestConsumerConnID() ConnectionID {
	return ConnectionID(fmt.Sprintf("%s:%s", "test-host", uuid.New().String()[:8]))
}

func TestChannelDeliveryState_SingleAck(t *testing.T) {
	var options = VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	}
	vh := NewVhost("test-vhost", options)
	vh.SetMetricsCollector(metrics.NewCollector(nil, context.Background()))

	// Fake connection (nil is sufficient for HandleBasicAck)
	// var conn net.Conn = nil
	connID := newTestConsumerConnID()

	// Register consumer (manual ack)
	c := newTestConsumer(connID, 1, "q1", false)

	// Manually simulate two deliveries by recording unacked entries
	key := ConnectionChannelKey{ConnectionID: connID, Channel: c.Channel}
	ch := &ChannelDeliveryState{
		UnackedByTag:      make(map[uint64]*DeliveryRecord),
		UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
	}
	vh.mu.Lock()
	vh.ChannelDeliveries[key] = ch
	vh.mu.Unlock()

	// Simulate two delivered messages (manual ack)
	m1 := Message{ID: "m1", Body: []byte("m1")}
	m2 := Message{ID: "m2", Body: []byte("m2")}

	ch.mu.Lock()
	ch.LastDeliveryTag++
	t1 := ch.LastDeliveryTag
	record := &DeliveryRecord{DeliveryTag: t1, ConsumerTag: c.Tag, QueueName: c.QueueName, Message: m1}
	ch.UnackedByTag[t1] = record
	if ch.UnackedByConsumer[c.Tag] == nil {
		ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
	}
	ch.UnackedByConsumer[c.Tag][t1] = record

	ch.LastDeliveryTag++
	t2 := ch.LastDeliveryTag
	record = &DeliveryRecord{DeliveryTag: t2, ConsumerTag: c.Tag, QueueName: c.QueueName, Message: m2}
	ch.UnackedByTag[t2] = record
	if ch.UnackedByConsumer[c.Tag] == nil {
		ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
	}
	ch.UnackedByConsumer[c.Tag][t2] = record
	ch.mu.Unlock()

	if len(ch.UnackedByTag) != 2 {
		t.Fatalf("expected 2 unacked, got %d", len(ch.UnackedByTag))
	}

	// Ack first only (single)
	if err := vh.HandleBasicAck(connID, c.Channel, t1, false); err != nil {
		t.Fatalf("HandleBasicAck error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if _, ok := ch.UnackedByTag[t1]; ok {
		t.Fatalf("delivery tag %d should be removed after ack", t1)
	}
	if _, ok := ch.UnackedByTag[t2]; !ok {
		t.Fatalf("delivery tag %d should remain unacked", t2)
	}
}

func TestChannelDeliveryState_MultipleAck(t *testing.T) {
	var options = VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	}
	vh := NewVhost("test-vhost", options)
	vh.SetMetricsCollector(metrics.NewCollector(&metrics.Config{Enabled: false}, context.Background()))
	connID := newTestConsumerConnID()
	key := ConnectionChannelKey{ConnectionID: connID, Channel: 1}
	ch := &ChannelDeliveryState{
		UnackedByTag:      make(map[uint64]*DeliveryRecord),
		UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
	}

	vh.mu.Lock()
	vh.ChannelDeliveries[key] = ch
	vh.mu.Unlock()
	// Register consumer
	c := newTestConsumer(connID, 1, "q-multi", false)
	// Simulate deliveries to track unacked messages

	// create 3 tags
	ch.mu.Lock()
	for i := 0; i < 3; i++ {
		ch.LastDeliveryTag++
		tag := ch.LastDeliveryTag
		record := &DeliveryRecord{DeliveryTag: tag}
		ch.UnackedByTag[tag] = record
		if ch.UnackedByConsumer[c.Tag] == nil {
			ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
		}
	}
	ch.mu.Unlock()

	if len(ch.UnackedByTag) != 3 {
		t.Fatalf("expected 3 unacked, got %d", len(ch.UnackedByTag))
	}

	// multiple ack up to second tag
	upTo := uint64(2)
	if err := vh.HandleBasicAck(connID, 1, upTo, true); err != nil {
		t.Fatalf("HandleBasicAck error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.UnackedByTag) != 1 {
		t.Fatalf("expected 1 unacked remaining, got %d", len(ch.UnackedByTag))
	}
	if _, ok := ch.UnackedByTag[3]; !ok {
		t.Fatalf("expected tag 3 to remain")
	}
}

func TestChannelDeliveryState_UnknownTag(t *testing.T) {
	var options = VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	}
	vh := NewVhost("test-vhost", options)
	connID := newTestConsumerConnID()
	// Ack when there is no state — should return error in current implementation
	if err := vh.HandleBasicAck(connID, 1, 42, false); err == nil {
		t.Fatalf("expected error when channel delivery state is missing")
	}
}

func TestDeliverTracking_NoAckFlag(t *testing.T) {
	var options = VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	}
	vh := NewVhost("test-vhost", options)
	connID := newTestConsumerConnID()
	key := ConnectionChannelKey{ConnectionID: connID, Channel: 2}

	ch := &ChannelDeliveryState{
		UnackedByTag:      make(map[uint64]*DeliveryRecord),
		UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
	}
	vh.mu.Lock()
	vh.ChannelDeliveries[key] = ch
	vh.mu.Unlock()

	// Register consumer
	c := newTestConsumer(connID, 2, "q-noack", false)

	// Simulate manual-ack delivery — stored
	ch.mu.Lock()
	ch.LastDeliveryTag++
	tag1 := ch.LastDeliveryTag
	ch.UnackedByTag[tag1] = &DeliveryRecord{DeliveryTag: tag1}
	if ch.UnackedByConsumer[c.Tag] == nil {
		ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
	}
	ch.UnackedByConsumer[c.Tag][tag1] = ch.UnackedByTag[tag1]
	ch.mu.Unlock()

	// Simulate auto-ack delivery — not stored
	ch.mu.Lock()
	ch.LastDeliveryTag++
	// intentionally do NOT store second tag to simulate NoAck=true
	ch.mu.Unlock()

	if len(ch.UnackedByTag) != 1 {
		t.Fatalf("expected only 1 unacked stored (manual ack), got %d", len(ch.UnackedByTag))
	}
}

func TestCleanupChannel_RequeuesUnacked(t *testing.T) {
	var options = VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	}
	vh := NewVhost("test-vhost", options)
	vh.SetMetricsCollector(metrics.NewCollector(&metrics.Config{Enabled: false}, context.Background()))
	connID := newTestConsumerConnID()
	// Create a queue
	q, err := vh.CreateQueue("q-clean", nil, connID)
	if err != nil {
		t.Fatalf("CreateQueue error: %v", err)
	}

	// Prepare an unacked record on channel (conn=nil, ch=3)
	key := ConnectionChannelKey{ConnectionID: connID, Channel: 3}
	ch := &ChannelDeliveryState{
		UnackedByTag:      make(map[uint64]*DeliveryRecord),
		UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
	}
	vh.mu.Lock()
	vh.ChannelDeliveries[key] = ch
	vh.mu.Unlock()

	// Register consumer
	c := newTestConsumer(connID, 3, q.Name, false)

	msg := Message{ID: "mx", Body: []byte("x")}
	ch.mu.Lock()
	ch.LastDeliveryTag = 1
	record := &DeliveryRecord{DeliveryTag: 1, QueueName: q.Name, Message: msg}
	ch.UnackedByTag[1] = record
	if ch.UnackedByConsumer[c.Tag] == nil {
		ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
	}
	ch.UnackedByConsumer[c.Tag][1] = record
	ch.mu.Unlock()

	// Cleanup should requeue the unacked message into q
	vh.CleanupChannel(connID, 3)

	if got := q.Len(); got != 1 {
		t.Fatalf("expected queue length 1 after requeue, got %d", got)
	}

	// ChannelDeliveries entry should be removed
	vh.mu.Lock()
	_, exists := vh.ChannelDeliveries[key]
	vh.mu.Unlock()
	if exists {
		t.Fatalf("expected ChannelDeliveries entry to be removed after cleanup")
	}
}

// newAckTestVhost builds a vhost with a channel holding n outstanding deliveries.
func newAckTestVhost(t *testing.T, channel uint16, n int) (*VHost, ConnectionID, *ChannelDeliveryState) {
	t.Helper()
	vh := NewVhost("test-vhost", VHostOptions{
		QueueBufferSize: 1000,
		Persistence:     nil,
	})
	vh.SetMetricsCollector(metrics.NewCollector(&metrics.Config{Enabled: false}, context.Background()))
	connID := newTestConsumerConnID()
	key := ConnectionChannelKey{ConnectionID: connID, Channel: channel}
	ch := &ChannelDeliveryState{
		UnackedByTag:      make(map[uint64]*DeliveryRecord),
		UnackedByConsumer: make(map[string]map[uint64]*DeliveryRecord),
	}
	c := newTestConsumer(connID, channel, "q-ack", false)
	for i := 0; i < n; i++ {
		ch.LastDeliveryTag++
		record := &DeliveryRecord{DeliveryTag: ch.LastDeliveryTag, ConsumerTag: c.Tag, QueueName: c.QueueName}
		ch.UnackedByTag[record.DeliveryTag] = record
		if ch.UnackedByConsumer[c.Tag] == nil {
			ch.UnackedByConsumer[c.Tag] = make(map[uint64]*DeliveryRecord)
		}
		ch.UnackedByConsumer[c.Tag][record.DeliveryTag] = record
	}
	vh.mu.Lock()
	vh.ChannelDeliveries[key] = ch
	vh.mu.Unlock()
	return vh, connID, ch
}

func requireChannelError(t *testing.T, err error, methodID uint16) *amqperr.ChannelError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a channel error, got nil")
	}
	channelErr, ok := err.(*amqperr.ChannelError)
	if !ok {
		t.Fatalf("expected *errors.ChannelError, got %T: %v", err, err)
	}
	if channelErr.ReplyCode() != uint16(amqp.PRECONDITION_FAILED) {
		t.Errorf("expected reply code %d, got %d", uint16(amqp.PRECONDITION_FAILED), channelErr.ReplyCode())
	}
	if channelErr.ClassID() != uint16(amqp.BASIC) {
		t.Errorf("expected class id %d, got %d", uint16(amqp.BASIC), channelErr.ClassID())
	}
	if channelErr.MethodID() != methodID {
		t.Errorf("expected method id %d, got %d", methodID, channelErr.MethodID())
	}
	return channelErr
}

func TestAckUnknownTagIsChannelError(t *testing.T) {
	vh, connID, ch := newAckTestVhost(t, 1, 1)

	err := vh.HandleBasicAck(connID, 1, 999, false)
	requireChannelError(t, err, uint16(amqp.BASIC_ACK))

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if _, exists := ch.UnackedByTag[1]; !exists {
		t.Fatalf("unknown tag ack must not remove outstanding delivery 1")
	}
}

func TestAckDoubleAckIsChannelError(t *testing.T) {
	vh, connID, ch := newAckTestVhost(t, 1, 1)

	if err := vh.HandleBasicAck(connID, 1, 1, false); err != nil {
		t.Fatalf("first ack failed: %v", err)
	}
	err := vh.HandleBasicAck(connID, 1, 1, false)
	requireChannelError(t, err, uint16(amqp.BASIC_ACK))

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.UnackedByTag) != 0 {
		t.Fatalf("expected no unacked deliveries, got %d", len(ch.UnackedByTag))
	}
}

func TestAckWithoutChannelStateIsChannelError(t *testing.T) {
	vh := NewVhost("test-vhost", VHostOptions{QueueBufferSize: 1000})
	connID := newTestConsumerConnID()

	err := vh.HandleBasicAck(connID, 1, 5, false)
	requireChannelError(t, err, uint16(amqp.BASIC_ACK))
}

func TestAckMultipleZeroAcksAllOutstanding(t *testing.T) {
	vh, connID, ch := newAckTestVhost(t, 1, 3)

	if err := vh.HandleBasicAck(connID, 1, 0, true); err != nil {
		t.Fatalf("multiple ack with tag 0 failed: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.UnackedByTag) != 0 {
		t.Fatalf("expected all deliveries acked, %d remain", len(ch.UnackedByTag))
	}
}

func TestAckMultipleUnknownTagIsChannelError(t *testing.T) {
	vh, connID, ch := newAckTestVhost(t, 1, 3)

	err := vh.HandleBasicAck(connID, 1, 99, true)
	requireChannelError(t, err, uint16(amqp.BASIC_ACK))

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.UnackedByTag) != 3 {
		t.Fatalf("failed multiple ack must not remove deliveries, %d remain", len(ch.UnackedByTag))
	}
}

func TestAckMultipleZeroWithoutChannelStateIsNoOp(t *testing.T) {
	vh := NewVhost("test-vhost", VHostOptions{QueueBufferSize: 1000})
	connID := newTestConsumerConnID()

	if err := vh.HandleBasicAck(connID, 1, 0, true); err != nil {
		t.Fatalf("multiple ack with tag 0 on an empty channel must be a no-op, got: %v", err)
	}
}

func TestNackUnknownTagReportsNackMethod(t *testing.T) {
	vh := NewVhost("test-vhost", VHostOptions{QueueBufferSize: 1000})
	connID := newTestConsumerConnID()

	err := vh.HandleBasicNack(connID, 1, 7, false, false)
	requireChannelError(t, err, uint16(amqp.BASIC_NACK))
}

func TestRejectUnknownTagReportsRejectMethod(t *testing.T) {
	vh := NewVhost("test-vhost", VHostOptions{QueueBufferSize: 1000})
	connID := newTestConsumerConnID()

	err := vh.HandleBasicReject(connID, 1, 7, false)
	requireChannelError(t, err, uint16(amqp.BASIC_REJECT))
}
