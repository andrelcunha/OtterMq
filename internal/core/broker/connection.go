package broker

import (
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/amqp/errors"
	"github.com/ottermq/ottermq/internal/core/broker/vhost"
	"github.com/rs/zerolog/log"
)

func (b *Broker) handleConnection(conn net.Conn, connInfo *amqp.ConnectionInfo) {
	b.ActiveConns.Add(1)
	client := connInfo.Client
	// ctx := client.Ctx

	defer func() {
		defer b.ActiveConns.Done()
		b.mu.Lock()
		_, exists := b.connInfos[conn]
		b.mu.Unlock()
		if !exists {
			log.Debug().Msg("No connections to clean")
			return
		}
		log.Debug().Msg("Cleaning connection")
		b.cleanupConnection(conn)
	}()
	// Registered last so it runs first: the cleanup above must not run while
	// a lock left held by the panic could block it.
	defer func() {
		if r := recover(); r != nil {
			b.handleConnectionPanic(conn, connInfo, "frame loop", r)
		}
	}()

	channelNum := uint16(0)

	for {
		frame, err := b.framer.ReadFrame(conn, client.Negotiated.FrameMax)
		var tooLarge *amqp.FrameTooLargeError
		if stderrors.As(err, &tooLarge) {
			// The rest of the oversized frame is still unread, so the stream
			// cannot be resynchronised: send connection.close and drop it.
			log.Warn().Str("client", conn.RemoteAddr().String()).Err(err).Msg("Frame exceeds frame_max")
			if _, sendErr := b.sendConnectionClosing(conn, 0, uint16(amqp.FRAME_ERROR), 0, 0, err.Error()); sendErr != nil {
				log.Debug().Err(sendErr).Msg("Failed to send connection.close")
			}
			client.CloseConn()
			return
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Debug().Err(err).Msg("Connection timeout")
			}
			if err == io.EOF || strings.Contains(err.Error(), "use of closed network connection") {
				log.Debug().Str("client", conn.RemoteAddr().String()).Msg("Connection closed by client")
			} else {
				log.Error().Err(err).Msg("Error reading frame")
			}
			client.Cancel()
			return
		}

		if len(frame) > 0 { // any octet shall be valid as heartbeat #AMQP_compliance
			if !b.registerHeartbeat(client) {
				log.Debug().Str("client", conn.RemoteAddr().String()).Msg("Connection already cleaned up, closing socket")
				client.CloseConn()
				return
			}
		}

		//Process frame
		newInterface, err := b.framer.ParseFrame(frame)
		if err != nil {
			log.Error().Err(err).Msg("Failed parsing frame")
			client.Cancel()
			return
		}
		if _, ok := newInterface.(*amqp.Heartbeat); ok {
			continue
		}

		newState, ok := newInterface.(*amqp.ChannelState)
		if !ok {
			log.Error().Msg("Failed to cast request to ChannelState")
			client.Cancel()
			return
		}

		log.Trace().Object("state", newState).Msg("New State")

		if newState.MethodFrame != nil {
			request := newState.MethodFrame
			if channelNum != request.Channel {
				channelNum = newState.MethodFrame.Channel
			}
		} else {
			if newState.HeaderFrame != nil {
				log.Trace().Interface("header", newState.HeaderFrame).Msg("HeaderFrame")
			} else if newState.Body != nil {
				log.Trace().Int("body_len", len(newState.Body)).Msg("Body")
			}

			// check if connection still exists
			if _, exists := b.getConnInfo(conn); !exists {
				log.Debug().Msg("Connection already cleaned up, stopping frame processing")
				client.Cancel()
				return
			}

			if previousState, exists := b.channelState(conn, channelNum); exists {
				newState.MethodFrame = previousState.MethodFrame
				log.Trace().Interface("method_frame", previousState.MethodFrame).Msg("Recovered method frame")
			} else {
				log.Trace().Uint16("channel", channelNum).Msg("Channel not found")
				continue
			}
		}
		if _, err := b.processRequest(conn, newState); err != nil {
			// if the error reaches here, it means that the error is at connection level and unexpected
			// Expected errors should be handled at method level and send channel.close instead
			if _, isChannelError := err.(*errors.ChannelError); isChannelError {
				log.Error().Err(err).Msg("Unexpected channel error at connection level")
			} else if _, isMethodError := err.(*errors.ConnectionError); isMethodError {
				log.Error().Err(err).Msg("Failed to process request")
			}
			request := newState.MethodFrame

			b.sendConnectionClosing(conn,
				request.Channel,
				uint16(amqp.INTERNAL_ERROR),
				uint16(request.ClassID),
				uint16(request.MethodID),
				err.Error(),
			)
			b.setConnectionClosingState(conn)
		}
	}
}

// registerConnection adds a connection that finished its handshake. It
// returns false, registering nothing, once the broker is shutting down. The
// check runs under b.mu, under which BroadcastConnectionClose also copies the
// connections, so a connection either gets the shutdown connection.close or
// is refused here.
func (b *Broker) registerConnection(conn net.Conn, connInfo *amqp.ConnectionInfo) bool {
	b.mu.Lock()
	if b.ShuttingDown.Load() {
		b.mu.Unlock()
		return false
	}
	b.connInfos[conn] = connInfo
	b.mu.Unlock()

	// Register connection ID in bidirectional maps
	connID := vhost.ConnectionID(GenerateConnectionID(conn))
	b.connectionsMu.Lock()
	b.connections[connID] = conn
	b.connToID[conn] = connID
	if connInfo.Client != nil && connInfo.Client.Config != nil && connInfo.Client.Config.ConsumerCancelNotify {
		if b.cancelNotify == nil {
			b.cancelNotify = make(map[vhost.ConnectionID]struct{})
		}
		b.cancelNotify[connID] = struct{}{}
	}
	b.connectionsMu.Unlock()

	b.collector.RecordConnection()
	return true
}

func (b *Broker) cleanupConnection(conn net.Conn) {
	b.mu.Lock()
	connInfo, ok := b.connInfos[conn]
	if !ok {
		b.mu.Unlock()
		log.Debug().Msg("cleanupConnection: connection not found in b.connInfos")
		return
	}
	vhName := connInfo.VHostName
	delete(b.connInfos, conn)
	b.mu.Unlock()

	// Remove from bidirectional maps
	b.connectionsMu.Lock()
	connID, hasConnID := b.connToID[conn]
	delete(b.connections, connID)
	delete(b.connToID, conn)
	delete(b.cancelNotify, connID)
	b.connectionsMu.Unlock()

	// Unblocks this connection's reader, which then exits through its own
	// (idempotent) cleanup path.
	connInfo.Client.CloseConn()
	vh := b.GetVHost(vhName)
	if vh != nil && hasConnID {
		vh.CleanupConnection(connID)
		log.Debug().Str("vhost", vhName).Str("conn_id", string(connID)).Msg("Cleaned up connection consumers and channels from vhost")
	}

	b.collector.RecordConnectionClose()
	connInfo.MarkCleanedUp()
}

// closeConnectionRequested closes a connection and sends a CONNECTION_CLOSE_OK frame
func (b *Broker) closeConnectionRequested(request *amqp.RequestMethodMessage, conn net.Conn) (any, error) {
	frame := b.framer.CreateConnectionCloseOkFrame(request.Channel)
	err := b.framer.SendFrame(conn, frame)
	b.mu.Lock()
	connInfo, exists := b.connInfos[conn]
	if !exists {
		b.mu.Unlock()
		return nil, nil
	}
	connInfo.ClosingConnection = true
	b.mu.Unlock()
	return nil, err
}

func (b *Broker) isConnectionClosing(conn net.Conn) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	connInfo, exists := b.connInfos[conn]
	if !exists {
		return false, fmt.Errorf("connection not found")
	}
	return connInfo.ClosingConnection, nil
}

// sendConnectionClosing sends `connection.close` when the server needs to shutdown for some reason
func (b *Broker) sendConnectionClosing(conn net.Conn, channel, replyCode, methodId, classId uint16, replyText string) (any, error) {
	frame := b.framer.CreateConnectionCloseFrame(channel, replyCode, methodId, classId, replyText)
	err := b.framer.SendFrame(conn, frame)

	return nil, err
}

func (b *Broker) connectionCloseOk(conn net.Conn) {
	b.cleanupConnection(conn) // closes the socket
}

// registerHeartbeat registers a heartbeat for a connection. It returns false
// if the connection is being torn down: cleanupConnection cancels the
// client's context, so no b.mu round-trip is needed on every frame.
func (b *Broker) registerHeartbeat(client *amqp.AmqpClient) bool {
	if client.Ctx.Err() != nil {
		return false
	}
	client.TouchHeartbeat()
	return true
}

// broadcastCloseTimeout bounds the shutdown connection.close write to one
// client, so a client that has stopped reading can't stall shutdown.
const broadcastCloseTimeout = time.Second

// BroadcastConnectionClose sends connection.close (320 CONNECTION_FORCED) to
// every connection. The connections are copied under b.mu and written to
// after it is released, in parallel, each with a write deadline. A client
// whose write times out is disconnected, since its stream may now hold a
// partial frame.
func (b *Broker) BroadcastConnectionClose() {
	type target struct {
		conn   net.Conn
		client *amqp.AmqpClient
	}
	b.mu.Lock()
	targets := make([]target, 0, len(b.connInfos))
	for conn, connInfo := range b.connInfos {
		targets = append(targets, target{conn, connInfo.Client})
	}
	b.mu.Unlock()

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = t.conn.SetWriteDeadline(time.Now().Add(broadcastCloseTimeout))
			_, err := b.sendConnectionClosing(t.conn, 0, uint16(amqp.CONNECTION_FORCED), 0, 0, amqp.ReplyText[amqp.CONNECTION_FORCED])
			if err == nil {
				// The connection keeps running until the client answers.
				_ = t.conn.SetWriteDeadline(time.Time{})
				return
			}
			log.Error().Err(err).Msg("Failed to send close connection")
			if t.client != nil {
				t.client.CloseConn()
			}
		}()
	}
	wg.Wait()
}

func (b *Broker) connectionHandler(request *amqp.RequestMethodMessage, conn net.Conn) (any, error) {
	switch request.MethodID {
	case uint16(amqp.CONNECTION_CLOSE):
		return b.closeConnectionRequested(request, conn)
	case uint16(amqp.CONNECTION_CLOSE_OK):
		b.connectionCloseOk(conn)
		return nil, nil
	default:
		log.Debug().Uint16("method_id", request.MethodID).Msg("Unknown connection method")
		return nil, fmt.Errorf("unknown connection method: %d", request.MethodID)
	}
}

// sendChannelClosing sends `channel.close` when the server raises a channel exception or needs to close the channel for some reason
func (b *Broker) sendChannelClosing(conn net.Conn, channel, replyCode, classId, methodId uint16, replyText string) error {
	b.mu.Lock()
	connInfo, exists := b.connInfos[conn]
	if !exists {
		b.mu.Unlock()
		return fmt.Errorf("connection not found")
	}
	chState, exists := connInfo.Channels[channel]
	if !exists {
		b.mu.Unlock()
		return fmt.Errorf("channel %d not found", channel)
	}
	chState.ClosingChannel = true
	b.mu.Unlock()
	frame := b.framer.CreateChannelCloseFrame(channel, replyCode, classId, methodId, replyText)
	return b.framer.SendFrame(conn, frame)
}
