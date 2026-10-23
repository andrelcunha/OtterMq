package broker

import (
	"net"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/ottermq/ottermq/internal/core/broker/vhost"
)

// Accessors for the maps guarded by b.mu and b.connectionsMu (see Broker).
// A connection can be removed by another goroutine at any time (management
// close, heartbeat timeout, shutdown), so every lookup reports whether the
// entry still exists instead of assuming it does.

// NewBrokerForVHosts returns a broker that serves the given vhosts and has no
// listener or connections. It is meant for tests of the layers built on the
// broker, such as the management service and the HTTP handlers.
func NewBrokerForVHosts(vhosts ...*vhost.VHost) *Broker {
	b := &Broker{
		vhosts:      make(map[string]*vhost.VHost, len(vhosts)),
		connInfos:   make(map[net.Conn]*amqp.ConnectionInfo),
		connections: make(map[vhost.ConnectionID]net.Conn),
		connToID:    make(map[net.Conn]vhost.ConnectionID),
	}
	for _, vh := range vhosts {
		b.vhosts[vh.Name] = vh
	}
	return b
}

// getConnInfo returns the registered info for conn.
func (b *Broker) getConnInfo(conn net.Conn) (*amqp.ConnectionInfo, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	connInfo, ok := b.connInfos[conn]
	return connInfo, ok
}

// channelState returns conn's state for channel.
func (b *Broker) channelState(conn net.Conn, channel uint16) (*amqp.ChannelState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.channelStateLocked(conn, channel)
}

// channelStateLocked is channelState for callers that hold b.mu.
func (b *Broker) channelStateLocked(conn net.Conn, channel uint16) (*amqp.ChannelState, bool) {
	connInfo, ok := b.connInfos[conn]
	if !ok {
		return nil, false
	}
	state, ok := connInfo.Channels[channel]
	return state, ok
}

// updateChannelState runs update on conn's state for channel under b.mu. It
// reports false, without calling update, if the connection or channel is gone.
func (b *Broker) updateChannelState(conn net.Conn, channel uint16, update func(*amqp.ChannelState)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.channelStateLocked(conn, channel)
	if !ok || state == nil {
		return false
	}
	update(state)
	return true
}

// resetChannelState replaces conn's state for channel with an empty one, ready
// for the next publish. It does nothing if the connection is gone.
func (b *Broker) resetChannelState(conn net.Conn, channel uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if connInfo, ok := b.connInfos[conn]; ok {
		connInfo.Channels[channel] = &amqp.ChannelState{}
	}
}

// connUser returns the user conn authenticated as, or "" if it is gone.
func (b *Broker) connUser(conn net.Conn) string {
	connInfo, ok := b.getConnInfo(conn)
	if !ok || connInfo.Client == nil || connInfo.Client.Config == nil {
		return ""
	}
	return connInfo.Client.Config.Username
}

// connByID returns the connection registered under connID.
func (b *Broker) connByID(connID vhost.ConnectionID) (net.Conn, bool) {
	b.connectionsMu.RLock()
	defer b.connectionsMu.RUnlock()
	conn, ok := b.connections[connID]
	return conn, ok
}
