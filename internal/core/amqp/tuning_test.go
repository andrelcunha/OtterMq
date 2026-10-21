package amqp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestNewServerTuning(t *testing.T) {
	tests := []struct {
		name     string
		frameMax uint32
		want     uint32
	}{
		{"configured value kept", 131072, 131072},
		{"0 becomes the default", 0, DEFAULT_FRAME_MAX},
		{"below the spec minimum is raised", 1024, FRAME_MIN_SIZE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewServerTuning(2048, tt.frameMax, 60)
			if got.FrameMax != tt.want || got.ChannelMax != 2048 || got.Heartbeat != 60 {
				t.Errorf("NewServerTuning(2048, %d, 60) = %+v, want frame-max %d", tt.frameMax, got, tt.want)
			}
		})
	}
}

func TestServerTuningNegotiate(t *testing.T) {
	server := ServerTuning{ChannelMax: 2048, FrameMax: 131072, Heartbeat: 60}

	tests := []struct {
		name    string
		server  ServerTuning
		tuneOk  ConnectionTune
		want    Negotiated
		wantErr bool
	}{
		{"client values within limits", server,
			ConnectionTune{ChannelMax: 100, FrameMax: 8192, Heartbeat: 10},
			Negotiated{ChannelMax: 100, FrameMax: 8192, Heartbeat: 10}, false},
		{"channel-max 0 means the server's", server,
			ConnectionTune{ChannelMax: 0, FrameMax: 8192, Heartbeat: 10},
			Negotiated{ChannelMax: 2048, FrameMax: 8192, Heartbeat: 10}, false},
		{"channel-max above the server's is clamped", server,
			ConnectionTune{ChannelMax: 65535, FrameMax: 8192, Heartbeat: 10},
			Negotiated{ChannelMax: 2048, FrameMax: 8192, Heartbeat: 10}, false},
		{"server channel-max 0 means no limit", ServerTuning{FrameMax: 131072},
			ConnectionTune{ChannelMax: 0, FrameMax: 8192},
			Negotiated{ChannelMax: 65535, FrameMax: 8192}, false},
		{"frame-max 0 means the server's", server,
			ConnectionTune{ChannelMax: 100, FrameMax: 0, Heartbeat: 10},
			Negotiated{ChannelMax: 100, FrameMax: 131072, Heartbeat: 10}, false},
		{"frame-max at the spec minimum", server,
			ConnectionTune{ChannelMax: 100, FrameMax: FRAME_MIN_SIZE, Heartbeat: 10},
			Negotiated{ChannelMax: 100, FrameMax: FRAME_MIN_SIZE, Heartbeat: 10}, false},
		{"frame-max below the spec minimum", server,
			ConnectionTune{ChannelMax: 100, FrameMax: FRAME_MIN_SIZE - 1, Heartbeat: 10},
			Negotiated{}, true},
		{"frame-max above the server's", server,
			ConnectionTune{ChannelMax: 100, FrameMax: 131073, Heartbeat: 10},
			Negotiated{}, true},
		{"heartbeat 0 disables heartbeats", server,
			ConnectionTune{ChannelMax: 100, FrameMax: 8192, Heartbeat: 0},
			Negotiated{ChannelMax: 100, FrameMax: 8192, Heartbeat: 0}, false},
		{"heartbeat above the server's is capped", server,
			ConnectionTune{ChannelMax: 100, FrameMax: 8192, Heartbeat: 600},
			Negotiated{ChannelMax: 100, FrameMax: 8192, Heartbeat: 60}, false},
		{"server heartbeat 0 leaves the client's", ServerTuning{ChannelMax: 2048, FrameMax: 131072},
			ConnectionTune{ChannelMax: 100, FrameMax: 8192, Heartbeat: 600},
			Negotiated{ChannelMax: 100, FrameMax: 8192, Heartbeat: 600}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.server.Negotiate(tt.tuneOk)
			if tt.wantErr {
				var tuneErr *TuneError
				if !errors.As(err, &tuneErr) || tuneErr.ReplyCode != NOT_ALLOWED {
					t.Fatalf("Negotiate(%+v) error = %v, want a 530 TuneError", tt.tuneOk, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Negotiate(%+v) error = %v", tt.tuneOk, err)
			}
			if got != tt.want {
				t.Errorf("Negotiate(%+v) = %+v, want %+v", tt.tuneOk, got, tt.want)
			}
		})
	}
}

func TestHeartbeatSendInterval(t *testing.T) {
	tests := []struct {
		heartbeat uint16
		want      time.Duration
	}{
		{1, time.Second}, // used to be 1>>1 = 0 s, which panicked NewTicker
		{2, time.Second},
		{3, 1500 * time.Millisecond},
		{60, 30 * time.Second},
	}
	for _, tt := range tests {
		if got := heartbeatSendInterval(tt.heartbeat); got != tt.want {
			t.Errorf("heartbeatSendInterval(%d) = %s, want %s", tt.heartbeat, got, tt.want)
		}
	}
}

// A negotiated heartbeat of 0 starts no heartbeat goroutines: the monitor
// would otherwise time the connection out on its first tick (#177).
func TestStartHeartbeatDisabled(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewAmqpClient(server, &AmqpClientConfig{}, ctx, cancel)
	c.StartHeartbeat()

	time.Sleep(1500 * time.Millisecond) // past the monitor's first tick
	if ctx.Err() != nil {
		t.Fatal("connection cancelled with heartbeats disabled")
	}
}

// splitBodyFrames parses consecutive body frames and returns their payloads.
func splitBodyFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var payloads [][]byte
	for len(data) > 0 {
		if len(data) < 8 || data[0] != byte(TYPE_BODY) {
			t.Fatalf("malformed body frame data: % x", data[:min(len(data), 8)])
		}
		size := int(binary.BigEndian.Uint32(data[3:7]))
		if len(data) < 7+size+1 || data[7+size] != FRAME_END {
			t.Fatalf("truncated body frame of declared size %d", size)
		}
		payloads = append(payloads, data[7:7+size])
		data = data[7+size+1:]
	}
	return payloads
}

func TestCreateBodyFrameSplitsAtFrameMax(t *testing.T) {
	const frameMax = 4096
	chunk := frameMax - FRAME_OVERHEAD

	tests := []struct {
		name       string
		bodySize   int
		frameMax   uint32
		wantFrames int
	}{
		{"empty body", 0, frameMax, 1},
		{"fits in one frame", chunk, frameMax, 1},
		{"one octet over", chunk + 1, frameMax, 2},
		{"3 x frame-max", 3 * frameMax, frameMax, 4},
		{"frame-max 0 means no limit", 3 * frameMax, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := make([]byte, tt.bodySize)
			for i := range body {
				body[i] = byte(i)
			}
			payloads := splitBodyFrames(t, createBodyFrame(7, tt.frameMax, body))
			if len(payloads) != tt.wantFrames {
				t.Fatalf("got %d body frames, want %d", len(payloads), tt.wantFrames)
			}
			var joined []byte
			for _, p := range payloads {
				if tt.frameMax != 0 && len(p)+FRAME_OVERHEAD > int(tt.frameMax) {
					t.Errorf("body frame of %d octets exceeds frame-max %d", len(p)+FRAME_OVERHEAD, tt.frameMax)
				}
				joined = append(joined, p...)
			}
			if !bytes.Equal(joined, body) {
				t.Error("reassembled body differs from the original")
			}
		})
	}
}

// readFrame must reject an oversized declared size from the header alone:
// the payload is never sent here, so a read of it would fail differently.
func TestReadFrameRejectsSizeAboveFrameMaxBeforeReading(t *testing.T) {
	const frameMax = 4096
	header := []byte{byte(TYPE_BODY), 0, 1}
	header = binary.BigEndian.AppendUint32(header, 2*frameMax) // 2 x frame-max

	_, err := readFrame(newMockConn(header), frameMax)
	var tooLarge *FrameTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("readFrame returned %v, want *FrameTooLargeError", err)
	}
	if tooLarge.PayloadSize != 2*frameMax || tooLarge.FrameMax != frameMax {
		t.Errorf("error = %+v, want payload %d and frame-max %d", tooLarge, 2*frameMax, frameMax)
	}
}
