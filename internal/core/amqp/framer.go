package amqp

import (
	"context"
	"net"
)

type Framer interface {
	ReadFrame(conn net.Conn, frameMax uint32) ([]byte, error)
	SendFrame(conn net.Conn, frame []byte) error
	Handshake(cfg HandshakeConfig, conn net.Conn, connCtxt context.Context) (*ConnectionInfo, error)
	ParseFrame(frame []byte) (any, error)
	CreateHeaderFrame(channel, classID uint16, msg Message) ([]byte, error)
	// CreateBodyFrame splits content into body frames of at most frameMax
	// octets each (header and frame-end included).
	CreateBodyFrame(channel uint16, frameMax uint32, content []byte) []byte

	// Basic Methods

	CreateBasicQosOkFrame(channel uint16) ([]byte, error)
	CreateBasicDeliverFrame(channel uint16, consumerTag, exchange, routingKey string, deliveryTag uint64, redelivered bool) ([]byte, error)
	CreateBasicReturnFrame(channel uint16, replyCode uint16, replyText, exchange, routingKey string) ([]byte, error)
	CreateBasicGetEmptyFrame(channel uint16) ([]byte, error)
	CreateBasicGetOkFrame(channel uint16, exchange, routingkey string, msgCount uint32, deliveryTag uint64, redelivered bool) ([]byte, error)
	CreateBasicConsumeOkFrame(channel uint16, consumerTag string) ([]byte, error)
	CreateBasicCancelOkFrame(channel uint16, consumerTag string) ([]byte, error)
	// CreateBasicCancelFrame builds a server-sent basic.cancel (nowait) for
	// clients that advertise consumer_cancel_notify.
	CreateBasicCancelFrame(channel uint16, consumerTag string) ([]byte, error)
	CreateBasicRecoverOkFrame(channel uint16) ([]byte, error)

	// Queue Methods

	CreateQueueDeclareOkFrame(channel uint16, queueName string, messageCount, consumerCount uint32) ([]byte, error)
	CreateQueueBindOkFrame(channel uint16) ([]byte, error)
	CreateQueueUnbindOkFrame(channel uint16) ([]byte, error)
	CreateQueueDeleteOkFrame(channel uint16, messageCount uint32) ([]byte, error)
	CreateQueuePurgeOkFrame(channel uint16, messageCount uint32) ([]byte, error)

	// Exchange Methods

	CreateExchangeDeclareFrameOk(channel uint16) ([]byte, error)
	CreateExchangeDeleteFrame(channel uint16) ([]byte, error)

	// Channel Methods

	CreateChannelOpenOkFrame(channel uint16) ([]byte, error)
	CreateChannelFlowFrame(channel uint16, active bool) ([]byte, error)
	CreateChannelFlowOkFrame(channel uint16, active bool) ([]byte, error)
	CreateChannelCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error)
	CreateChannelCloseOkFrame(channel uint16) ([]byte, error)

	// Connection Methods

	CreateConnectionCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error)
	CreateConnectionCloseOkFrame(channel uint16) ([]byte, error)

	// Transaction Methods

	CreateTxSelectOkFrame(channel uint16) ([]byte, error)
	CreateTxCommitOkFrame(channel uint16) ([]byte, error)
	CreateTxRollbackOkFrame(channel uint16) ([]byte, error)
}

type DefaultFramer struct{}

func (d *DefaultFramer) ReadFrame(conn net.Conn, frameMax uint32) ([]byte, error) {
	return readFrame(conn, frameMax)
}

func (d *DefaultFramer) SendFrame(conn net.Conn, frame []byte) error {
	return sendFrame(conn, frame)
}

func (d *DefaultFramer) Handshake(cfg HandshakeConfig, conn net.Conn, connCtxt context.Context) (*ConnectionInfo, error) {
	return handshake(cfg, conn, connCtxt)
}

func (d *DefaultFramer) ParseFrame(frame []byte) (any, error) {
	return parseFrame(frame)
}

func (d *DefaultFramer) CreateHeaderFrame(channel, classID uint16, msg Message) ([]byte, error) {
	return createHeaderFrame(channel, classID, msg)
}

func (d *DefaultFramer) CreateBodyFrame(channel uint16, frameMax uint32, content []byte) []byte {
	return createBodyFrame(channel, frameMax, content)
}

// REGION Queue Methods

func (d *DefaultFramer) CreateQueueDeclareOkFrame(channel uint16, queueName string, messageCount, consumerCount uint32) ([]byte, error) {
	return createQueueDeclareOkFrame(channel, queueName, messageCount, consumerCount)
}

func (d *DefaultFramer) CreateQueueBindOkFrame(channel uint16) ([]byte, error) {
	return createQueueAckFrame(channel, uint16(QUEUE_BIND_OK))
}

func (d *DefaultFramer) CreateQueueUnbindOkFrame(channel uint16) ([]byte, error) {
	return createQueueAckFrame(channel, uint16(QUEUE_UNBIND_OK))
}

func (d *DefaultFramer) CreateQueuePurgeOkFrame(channel uint16, messageCount uint32) ([]byte, error) {
	return createQueueCountAckFrame(channel, uint16(QUEUE_PURGE_OK), messageCount)
}

func (d *DefaultFramer) CreateQueueDeleteOkFrame(channel uint16, messageCount uint32) ([]byte, error) {
	return createQueueCountAckFrame(channel, uint16(QUEUE_DELETE_OK), messageCount)
}

// ENDREGION

// REGION Exchange Methods

func (d *DefaultFramer) CreateExchangeDeclareFrameOk(channel uint16) ([]byte, error) {
	return createExchangeDeclareFrameOk(channel)
}

func (d *DefaultFramer) CreateExchangeDeleteFrame(channel uint16) ([]byte, error) {
	return createExchangeDeleteFrame(channel)
}

// ENDREGION

// REGION Channel Methods

func (d *DefaultFramer) CreateChannelOpenOkFrame(channel uint16) ([]byte, error) {
	return createChannelOpenOkFrame(channel)
}

func (d *DefaultFramer) CreateChannelFlowFrame(channel uint16, active bool) ([]byte, error) {
	return createChannelFlowFrame(channel, active)
}

func (d *DefaultFramer) CreateChannelFlowOkFrame(channel uint16, active bool) ([]byte, error) {
	return createChannelFlowOkFrame(channel, active)
}

func (d *DefaultFramer) CreateChannelCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error) {
	return createChannelCloseFrame(channel, replyCode, classID, methodID, replyText)
}

func (d *DefaultFramer) CreateChannelCloseOkFrame(channel uint16) ([]byte, error) {
	return createChannelCloseOkFrame(channel)
}

// ENDREGION

// REGION Basic Methods

func (d *DefaultFramer) CreateBasicQosOkFrame(channel uint16) ([]byte, error) {
	return createBasicQosOkFrame(channel)
}

func (d *DefaultFramer) CreateBasicDeliverFrame(channel uint16, consumerTag, exchange, routingKey string, deliveryTag uint64, redelivered bool) ([]byte, error) {
	return createBasicDeliverFrame(channel, consumerTag, exchange, routingKey, deliveryTag, redelivered)
}

func (d *DefaultFramer) CreateBasicReturnFrame(channel uint16, replyCode uint16, replyText, exchange, routingKey string) ([]byte, error) {
	return createBasicReturnFrame(channel, replyCode, replyText, exchange, routingKey)
}

func (d *DefaultFramer) CreateBasicConsumeOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	return createBasicConsumeOkFrame(channel, consumerTag)
}

func (d *DefaultFramer) CreateBasicCancelFrame(channel uint16, consumerTag string) ([]byte, error) {
	return createBasicCancelFrame(channel, consumerTag)
}

func (d *DefaultFramer) CreateBasicCancelOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	return createBasicCancelOkFrame(channel, consumerTag)
}

func (d *DefaultFramer) CreateBasicGetEmptyFrame(channel uint16) ([]byte, error) {
	return createBasicGetEmptyFrame(channel)
}

func (d *DefaultFramer) CreateBasicGetOkFrame(channel uint16, exchange, routingkey string, msgCount uint32, deliveryTag uint64, redelivered bool) ([]byte, error) {
	return createBasicGetOkFrame(channel, exchange, routingkey, msgCount, deliveryTag, redelivered)
}

func (d *DefaultFramer) CreateBasicRecoverOkFrame(channel uint16) ([]byte, error) {
	return createBasicRecoverOkFrame(channel)
}

// ENDREGION

// REGION Connection Methods

func (d *DefaultFramer) CreateConnectionCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error) {
	return createConnectionCloseFrame(channel, replyCode, classID, methodID, replyText)
}

func (d *DefaultFramer) CreateConnectionCloseOkFrame(channel uint16) ([]byte, error) {
	return createConnectionCloseOkFrame(channel)
}

// ENDREGION

// REGION Transaction Methods

func (d *DefaultFramer) CreateTxSelectOkFrame(channel uint16) ([]byte, error) {
	return createTxAckFrame(channel, uint16(TX_SELECT_OK))
}

func (d *DefaultFramer) CreateTxCommitOkFrame(channel uint16) ([]byte, error) {
	return createTxAckFrame(channel, uint16(TX_COMMIT_OK))
}

func (d *DefaultFramer) CreateTxRollbackOkFrame(channel uint16) ([]byte, error) {
	return createTxAckFrame(channel, uint16(TX_ROLLBACK_OK))
}

// ENDREGION
