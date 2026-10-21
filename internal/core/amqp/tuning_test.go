package amqp

import (
	"context"
	"net"
	"testing"
	"time"
)

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
