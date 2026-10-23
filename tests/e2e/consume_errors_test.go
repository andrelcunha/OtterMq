package e2e

import (
	"fmt"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// A refused basic.consume is a channel exception: it closes the channel and
// leaves the connection open. It used to close the connection with 541 too.
func TestBasicConsume_ErrorClosesOnlyTheChannel(t *testing.T) {
	conn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}

	missing := fmt.Sprintf("e2e.consume.missing.%d", time.Now().UnixNano())
	_, err = ch.Consume(missing, "", true, false, false, false, nil)
	amqpErr, ok := err.(*amqp.Error)
	if !ok || amqpErr.Code != amqp.NotFound {
		t.Fatalf("got %v, want 404 NOT_FOUND", err)
	}

	// The connection is still usable.
	ch2, err := conn.Channel()
	if err != nil {
		t.Fatalf("connection closed after a channel error: %v", err)
	}
	defer ch2.Close()
	if _, err := ch2.QueueDeclare("", false, true, true, false, nil); err != nil {
		t.Fatalf("queue.declare on a new channel: %v", err)
	}
	if conn.IsClosed() {
		t.Fatal("connection closed after a channel error")
	}
}
