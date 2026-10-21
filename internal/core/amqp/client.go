package amqp

import (
	"context"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

type AmqpClient struct {
	RemoteAddr    string
	ConnectedAt   time.Time
	LastHeartbeat time.Time
	Conn          net.Conn
	Ctx           context.Context
	Cancel        context.CancelFunc
	Config        *AmqpClientConfig

	recoveredPanic atomic.Bool
	closeOnce      sync.Once
}

type AmqpClientConfig struct {
	Username          string
	HeartbeatInterval uint16
	FrameMax          uint32
	ChannelMax        uint16
	Protocol          string
	SSL               bool
}

func NewAmqpClient(conn net.Conn, config *AmqpClientConfig, connCtx context.Context, cancel context.CancelFunc) *AmqpClient {

	client := &AmqpClient{
		RemoteAddr:  conn.RemoteAddr().String(),
		ConnectedAt: time.Now(),
		// Silence is measured from the handshake until the first frame.
		LastHeartbeat: time.Now(),
		Conn:          conn,
		Ctx:           connCtx,
		Cancel:        cancel,
		Config:        config,
	}

	return client
}

func NewAmqpClientConfig(configurations *map[string]any) *AmqpClientConfig {
	username := (*configurations)["username"].(string)
	heartbeatInterval := (*configurations)["heartbeatInterval"].(uint16)
	frameMax := (*configurations)["frameMax"].(uint32)
	channelMax := (*configurations)["channelMax"].(uint16)
	protocol := (*configurations)["protocol"].(string)
	ssl := (*configurations)["ssl"].(bool)

	return &AmqpClientConfig{
		Username:          username,
		HeartbeatInterval: heartbeatInterval,
		FrameMax:          frameMax,
		ChannelMax:        channelMax,
		Protocol:          protocol,
		SSL:               ssl,
	}
}

// ConnectionInfo represents the information of a connection to the AMQP server
type ConnectionInfo struct {
	VHostName         string                   `json:"vhost"`
	Client            *AmqpClient              `json:"client"`
	Channels          map[uint16]*ChannelState `json:"channels"`
	ClosingConnection bool                     `json:"closing_connection"`

	// Shared by copies; nil only for ConnectionInfo literals built in tests.
	cleanup *cleanupSignal
}

type cleanupSignal struct {
	once sync.Once
	done chan struct{}
}

// closedChan is returned by CleanedUp when there is no signal to wait on.
var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// MarkCleanedUp records that the broker finished cleaning up this
// connection. Safe to call more than once.
func (ci *ConnectionInfo) MarkCleanedUp() {
	if ci.cleanup != nil {
		ci.cleanup.once.Do(func() { close(ci.cleanup.done) })
	}
}

// CleanedUp is closed once the broker finished cleaning up this connection.
func (ci *ConnectionInfo) CleanedUp() <-chan struct{} {
	if ci.cleanup == nil {
		return closedChan
	}
	return ci.cleanup.done
}

// NewConnectionInfo creates a new ConnectionInfo, receiving the `vhost` name
func NewConnectionInfo(vhostName string) *ConnectionInfo {
	return &ConnectionInfo{
		VHostName: vhostName,
		Client:    nil,
		Channels:  make(map[uint16]*ChannelState),
		cleanup:   &cleanupSignal{done: make(chan struct{})},
	}
}

func (c *AmqpClient) StartHeartbeat() {
	go c.sendHeartbeats()
	go c.monitorHeartbeatTimeout()
}

// CloseConn cancels the connection's context and closes its socket. It is the
// one place the broker closes a client socket; later calls do nothing.
// Closing the socket is what unblocks a reader stuck in ReadFrame.
func (c *AmqpClient) CloseConn() {
	c.closeOnce.Do(func() {
		c.Cancel()
		if err := c.Conn.Close(); err != nil {
			log.Debug().Err(err).Str("client", c.RemoteAddr).Msg("Error closing connection")
		}
	})
}

// RecoveredPanic reports whether one of the client's heartbeat goroutines
// panicked and cancelled the connection.
func (c *AmqpClient) RecoveredPanic() bool {
	return c.recoveredPanic.Load()
}

// recoverHeartbeatPanic must be deferred directly by the heartbeat
// goroutines. A panic there cancels this connection and closes its socket,
// which unblocks the reader (or a handshake still in progress).
func (c *AmqpClient) recoverHeartbeatPanic(where string) {
	if r := recover(); r != nil {
		log.Error().
			Str("client", c.RemoteAddr).
			Str("where", where).
			Interface("panic", r).
			Str("stack", string(debug.Stack())).
			Msg("Recovered panic in heartbeat goroutine, closing connection")
		c.recoveredPanic.Store(true)
		c.CloseConn()
	}
}

func (c *AmqpClient) sendHeartbeats() {
	defer c.recoverHeartbeatPanic("sendHeartbeats")
	interval := time.Duration(c.Config.HeartbeatInterval>>1) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := sendHeartbeat(c.Conn)
			if err != nil {
				log.Error().Err(err).Msg("Heartbeat failed")
				return
			}
		case <-c.Ctx.Done():
			log.Debug().Msg("Heartbeat stopped due to context cancel")
			return
		}
	}
}

func (c *AmqpClient) monitorHeartbeatTimeout() {
	defer c.recoverHeartbeatPanic("monitorHeartbeatTimeout")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			maxInterval := time.Duration(c.Config.HeartbeatInterval<<1) * time.Second
			if time.Since(c.LastHeartbeat) > maxInterval {
				log.Warn().Str("addr", c.RemoteAddr).Msg("Heartbeat timeout")
				// Spec 4.2.7: close the socket without a close handshake.
				// Cleanup follows via monitorConnectionLifecycle.
				c.CloseConn()
				return
			}
		case <-c.Ctx.Done():
			return
		}
	}
}
