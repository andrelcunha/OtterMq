package amqp

import (
	"fmt"
	"time"
)

const (
	// FRAME_MIN_SIZE is the spec's frame-min-size: every peer must accept
	// frames of this size, so no frame-max may be smaller.
	FRAME_MIN_SIZE = 4096
	// DEFAULT_FRAME_MAX is used when the server is configured with frame-max
	// 0. "No limit" would let a client declare frames of up to 4 GiB.
	DEFAULT_FRAME_MAX = 131072
	// FRAME_OVERHEAD is the frame header (7 octets) plus the frame-end octet.
	FRAME_OVERHEAD = 8
)

// ServerTuning holds the limits the server offers in connection.tune. It is
// built once from the broker config and passed by value, so a handshake can
// never change what the next client is offered.
type ServerTuning struct {
	ChannelMax uint16 // 0 means no limit (65535)
	FrameMax   uint32
	Heartbeat  uint16 // seconds; also the cap on the client's value. 0 means no preference
}

// NewServerTuning builds the server's offer from the configured limits.
// frame-max 0 becomes DEFAULT_FRAME_MAX, and a value below the spec minimum
// is raised to FRAME_MIN_SIZE.
func NewServerTuning(channelMax uint16, frameMax uint32, heartbeat uint16) ServerTuning {
	switch {
	case frameMax == 0:
		frameMax = DEFAULT_FRAME_MAX
	case frameMax < FRAME_MIN_SIZE:
		frameMax = FRAME_MIN_SIZE
	}
	return ServerTuning{ChannelMax: channelMax, FrameMax: frameMax, Heartbeat: heartbeat}
}

// Negotiated holds the limits agreed for one connection in connection.tune-ok.
type Negotiated struct {
	ChannelMax uint16
	FrameMax   uint32
	Heartbeat  uint16 // seconds; 0 means heartbeats are disabled
}

// HandshakeConfig is everything the server needs to run a handshake. It is
// passed by value; ServerProperties, Mechanisms and Locales are read-only.
type HandshakeConfig struct {
	Protocol         string
	ServerProperties map[string]any
	Mechanisms       []string
	Locales          []string
	SSL              bool
	Tuning           ServerTuning
	// LoopbackUsers may only connect from a loopback address.
	LoopbackUsers []string
}

// TuneError is returned by Negotiate when the client's connection.tune-ok
// must be refused with connection.close.
type TuneError struct {
	ReplyCode ReplyCode
	Text      string
}

func (e *TuneError) Error() string { return e.Text }

// Negotiate applies the client's connection.tune-ok to the server's offer.
//   - channel-max: 0 or above the server's value means the server's value.
//   - frame-max: 0 means the server's value; below FRAME_MIN_SIZE or above
//     the server's value is refused with 530 NOT_ALLOWED.
//   - heartbeat: 0 disables heartbeats; otherwise the client's value, capped
//     at the server's when the server has one.
func (s ServerTuning) Negotiate(tuneOk ConnectionTune) (Negotiated, error) {
	n := Negotiated{
		ChannelMax: getSmalestShortInt(s.ChannelMax, tuneOk.ChannelMax),
		FrameMax:   tuneOk.FrameMax,
		Heartbeat:  tuneOk.Heartbeat,
	}

	if n.FrameMax == 0 {
		n.FrameMax = s.FrameMax
	}
	if n.FrameMax < FRAME_MIN_SIZE || n.FrameMax > s.FrameMax {
		return Negotiated{}, &TuneError{
			ReplyCode: NOT_ALLOWED,
			Text: fmt.Sprintf("frame_max=%d outside the allowed range %d-%d",
				tuneOk.FrameMax, FRAME_MIN_SIZE, s.FrameMax),
		}
	}

	if s.Heartbeat != 0 && n.Heartbeat > s.Heartbeat {
		n.Heartbeat = s.Heartbeat
	}
	return n, nil
}

// heartbeatSendInterval is how often the server sends heartbeats: half the
// negotiated interval, and never less than one second.
func heartbeatSendInterval(heartbeat uint16) time.Duration {
	interval := time.Duration(heartbeat) * time.Second / 2
	if interval < time.Second {
		return time.Second
	}
	return interval
}
