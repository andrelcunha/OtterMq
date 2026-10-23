package testutil

import (
	"context"
	"fmt"
	"net"

	"github.com/ottermq/ottermq/internal/core/amqp"
)

// MockFramer is a configurable test double for amqp.Framer
type MockFramer struct {
	SentFrames [][]byte // Records all sent frames
	SendError  error    // If non-nil, SendFrame returns this error
}

func (m *MockFramer) ReadFrame(conn net.Conn, frameMax uint32) ([]byte, error) { return nil, nil }

func (m *MockFramer) SendFrame(conn net.Conn, frame []byte) error {
	if m.SendError != nil {
		return m.SendError
	}
	m.SentFrames = append(m.SentFrames, frame)
	return nil
}

func (m *MockFramer) Handshake(cfg amqp.HandshakeConfig, conn net.Conn, connCtxt context.Context) (*amqp.ConnectionInfo, error) {
	return nil, nil
}

func (m *MockFramer) ParseFrame(frame []byte) (any, error) { return nil, nil }

func (m *MockFramer) CreateHeaderFrame(channel, classID uint16, msg amqp.Message) ([]byte, error) {
	return []byte("header-frame"), nil
}

func (m *MockFramer) CreateBodyFrame(channel uint16, frameMax uint32, content []byte) []byte {
	return []byte("body-frame")
}

func (m *MockFramer) CreateBasicReturnFrame(channel uint16, replyCode uint16, replyText, exchange, routingKey string) ([]byte, error) {
	return []byte("basic-return"), nil
}

func (m *MockFramer) CreateBasicQosOkFrame(channel uint16) ([]byte, error) {
	return []byte("basic-qos-ok"), nil
}

func (m *MockFramer) CreateBasicDeliverFrame(channel uint16, consumerTag, exchange, routingKey string, deliveryTag uint64, redelivered bool) ([]byte, error) {
	return []byte("basic-deliver"), nil
}

func (m *MockFramer) CreateBasicGetEmptyFrame(channel uint16) ([]byte, error) {
	return []byte("basic-get-empty"), nil
}

func (m *MockFramer) CreateBasicGetOkFrame(channel uint16, exchange, routingkey string, msgCount uint32, deliveryTag uint64, redelivered bool) ([]byte, error) {
	return []byte("basic-get-ok"), nil
}

func (m *MockFramer) CreateBasicConsumeOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	return []byte("basic-consume-ok:" + consumerTag), nil
}

func (m *MockFramer) CreateBasicCancelFrame(channel uint16, consumerTag string) ([]byte, error) {
	return []byte("basic-cancel:" + consumerTag), nil
}

func (m *MockFramer) CreateBasicCancelOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	return []byte("basic-cancel-ok:" + consumerTag), nil
}

func (m *MockFramer) CreateBasicRecoverOkFrame(channel uint16) ([]byte, error) {
	return []byte("basic-recover-ok"), nil
}

func (m *MockFramer) CreateQueueDeclareOkFrame(channel uint16, queueName string, messageCount, consumerCount uint32) ([]byte, error) {
	return []byte("queue-declare"), nil
}

func (m *MockFramer) CreateQueueBindOkFrame(channel uint16) ([]byte, error) {
	return []byte("queue-bind-ok"), nil
}

func (m *MockFramer) CreateQueueUnbindOkFrame(channel uint16) ([]byte, error) {
	return []byte("queue-unbind-ok"), nil
}

func (m *MockFramer) CreateQueueDeleteOkFrame(channel uint16, messageCount uint32) ([]byte, error) {
	return []byte("queue-delete-ok"), nil
}

func (m *MockFramer) CreateQueuePurgeOkFrame(channel uint16, messageCount uint32) ([]byte, error) {
	return []byte("queue-purge-ok"), nil
}

func (m *MockFramer) CreateExchangeDeclareFrameOk(channel uint16) ([]byte, error) {
	return []byte("exchange-declare"), nil
}

func (m *MockFramer) CreateExchangeDeleteFrame(channel uint16) ([]byte, error) {
	return []byte("exchange-delete"), nil
}

func (m *MockFramer) CreateChannelOpenOkFrame(channel uint16) ([]byte, error) {
	return []byte("channel-open-ok"), nil
}

func (m *MockFramer) CreateChannelFlowFrame(channel uint16, active bool) ([]byte, error) {
	return []byte("channel-flow"), nil
}

func (m *MockFramer) CreateChannelFlowOkFrame(channel uint16, active bool) ([]byte, error) {
	return []byte("channel-flow-ok"), nil
}

func (m *MockFramer) CreateChannelCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error) {
	return fmt.Appendf(nil, "channel-close %d %s", replyCode, replyText), nil
}

func (m *MockFramer) CreateChannelCloseOkFrame(channel uint16) ([]byte, error) {
	return []byte("channel-close-ok"), nil
}

func (m *MockFramer) CreateConnectionCloseOkFrame(channel uint16) ([]byte, error) {
	return []byte("connection-close-ok"), nil
}

func (m *MockFramer) CreateConnectionCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error) {
	return []byte("connection-close"), nil
}

func (m *MockFramer) CreateTxSelectOkFrame(channel uint16) ([]byte, error) {
	return []byte("tx-select-ok"), nil
}

func (m *MockFramer) CreateTxCommitOkFrame(channel uint16) ([]byte, error) {
	return []byte("tx-commit-ok"), nil
}

func (m *MockFramer) CreateTxRollbackOkFrame(channel uint16) ([]byte, error) {
	return []byte("tx-rollback-ok"), nil
}

// MockFrameSender is a test double for vhost.FrameSender interface
type MockFrameSender struct {
	SentFrames []SentFrame
	SendError  error
}

type SentFrame struct {
	ConnID  string
	Channel uint16
	Frame   []byte
}

// SendFrame implements the FrameSender interface
// Uses interface{} for connID to avoid importing vhost package
func (m *MockFrameSender) SendFrame(connID interface{}, channel uint16, frame []byte) error {
	if m.SendError != nil {
		return m.SendError
	}
	// Convert connID to string for recording
	connIDStr := ""
	if connID != nil {
		// Use Sprintf to convert any type to string safely
		connIDStr = fmt.Sprintf("%v", connID)
	}
	m.SentFrames = append(m.SentFrames, SentFrame{
		ConnID:  connIDStr,
		Channel: channel,
		Frame:   frame,
	})
	return nil
}
