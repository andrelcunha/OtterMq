package vhost

import (
	"strings"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
)

func TestDeliveryEncodingErrorWritesNoFrames(t *testing.T) {
	for _, tt := range []struct {
		name  string
		tag   string
		props amqp.BasicProperties
	}{
		{name: "method", tag: strings.Repeat("x", 256)},
		{name: "header", tag: "consumer", props: amqp.BasicProperties{MessageID: strings.Repeat("x", 256)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vh := setupTestVHost()
			vh.framer = &amqp.DefaultFramer{}
			sender := newGateSender(false)
			vh.SetFrameSender(sender)
			consumer := NewConsumer("synthetic-connection", 1, "queue", tt.tag, &ConsumerProperties{})
			consumer.Active.Store(true)
			err := vh.deliverToConsumer(consumer, Message{ID: "synthetic-message", Properties: tt.props}, false)
			if err == nil || !strings.Contains(err.Error(), "255 bytes") {
				t.Fatalf("expected shortstr length error, got %v", err)
			}
			if sender.deliveries.Load() != 0 {
				t.Fatal("encoding failure sent frames")
			}
			state := vh.getChannelDeliveryState(consumer.ConnectionID, consumer.Channel)
			state.mu.Lock()
			unacked := len(state.UnackedByTag) + len(state.UnackedByConsumer[tt.tag])
			state.mu.Unlock()
			if unacked != 0 {
				t.Errorf("failed delivery left %d unacked records", unacked)
			}
			if !consumer.sendMu.TryLock() {
				t.Fatal("failed delivery left the send mutex locked")
			}
			consumer.sendMu.Unlock()
		})
	}
}
