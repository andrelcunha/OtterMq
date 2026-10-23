package vhost

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ottermq/ottermq/internal/core/amqp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Publishing and popping a message logs its size, never its body.
func TestPublishAndPop_DoNotLogBody(t *testing.T) {
	const body = "message body that must stay out of the log"
	var buf bytes.Buffer
	logger, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = logger
		zerolog.SetGlobalLevel(level)
	})

	queue := &Queue{
		Name:     "orders",
		messages: make(chan Message, 1),
		Props:    &QueueProperties{},
	}
	vh := &VHost{
		Exchanges: map[string]*Exchange{
			"direct-ex": {
				Name:     "direct-ex",
				Typ:      DIRECT,
				Props:    &ExchangeProperties{},
				Bindings: map[string][]*Binding{"orders": {{Queue: queue}}},
			},
		},
		Queues: map[string]*Queue{"orders": queue},
	}
	msg := &Message{ID: uuid.New().String(), Body: []byte(body), Properties: amqp.BasicProperties{}}

	if _, err := vh.Publish("direct-ex", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if queue.Pop() == nil {
		t.Fatal("expected the message in the queue")
	}

	if !strings.Contains(buf.String(), `"body_size":42`) {
		t.Errorf("expected the body size in the log, got %q", buf.String())
	}
	if strings.Contains(buf.String(), body) {
		t.Errorf("the body was logged: %q", buf.String())
	}
}
