package broker

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"time"

	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/rs/zerolog/log"
)

// defaultPanicCleanupTimeout bounds the cleanup that runs after a recovered
// panic. Many broker handlers pair b.mu.Lock()/Unlock() by hand instead of
// using defer, so a panic between the two leaves b.mu locked and the cleanup
// can never finish. A broker whose global lock is stuck serves nobody, so we
// exit instead of hanging.
const defaultPanicCleanupTimeout = 10 * time.Second

func fatalAfterPanic(msg string) {
	log.Fatal().Msg(msg)
}

// handshakeGuarded runs the AMQP handshake and turns a panic into an error,
// so that one malformed handshake cannot take down the broker.
func (b *Broker) handshakeGuarded(configurations *map[string]any, conn net.Conn, connCtx context.Context) (connInfo *amqp.ConnectionInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			b.handleConnectionPanic(conn, nil, "handshake", r)
			connInfo, err = nil, fmt.Errorf("panic during handshake: %v", r)
		}
	}()
	return b.framer.Handshake(configurations, conn, connCtx)
}

// handleConnectionPanic logs a panic recovered in a goroutine serving conn,
// best-effort sends connection.close 541 INTERNAL_ERROR, cleans up the
// connection and closes the socket. Only conn is affected; the rest of the
// broker keeps running. connInfo is nil when the connection is not
// registered yet (handshake).
//
// Locks: the panicking handler may still hold b.mu, b.connectionsMu or a vhost
// lock it took without defer. The cleanup needs those locks, so it runs under
// a watchdog and the process exits if it does not finish in time.
func (b *Broker) handleConnectionPanic(conn net.Conn, connInfo *amqp.ConnectionInfo, where string, r any) {
	log.Error().
		Str("client", conn.RemoteAddr().String()).
		Str("conn_id", GenerateConnectionID(conn)).
		Str("where", where).
		Interface("panic", r).
		Str("stack", string(debug.Stack())).
		Msg("Recovered panic in connection goroutine, closing connection")
	b.collector.RecordConnectionPanic()

	// The peer may not be reading; don't let it block us.
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	frame := b.framer.CreateConnectionCloseFrame(0, uint16(amqp.INTERNAL_ERROR), 0, 0, amqp.ReplyText[amqp.INTERNAL_ERROR])
	if err := b.framer.SendFrame(conn, frame); err != nil {
		log.Debug().Err(err).Msg("Failed to send connection.close after panic")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.cleanupConnection(conn)
		if connInfo != nil {
			// Another goroutine (e.g. monitorConnectionLifecycle) may have
			// removed conn first, making the call above return early, and be
			// the one stuck on a lock the panic left held. Wait for it too.
			<-connInfo.CleanedUp()
		}
	}()
	select {
	case <-done:
	case <-time.After(b.panicCleanupTimeout):
		b.fatalAfterPanic(fmt.Sprintf("cleanup after a panic in %s did not finish within %s; a broker lock is probably held, exiting", where, b.panicCleanupTimeout))
	}

	if connInfo != nil {
		connInfo.Client.CloseConn()
	} else {
		conn.Close()
	}
}
