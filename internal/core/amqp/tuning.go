package amqp

import "time"

// heartbeatSendInterval is how often the server sends heartbeats: half the
// negotiated interval, and never less than one second.
func heartbeatSendInterval(heartbeat uint16) time.Duration {
	interval := time.Duration(heartbeat) * time.Second / 2
	if interval < time.Second {
		return time.Second
	}
	return interval
}
