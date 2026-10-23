package broker

import (
	"testing"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

// Deleting a queue drops its consumers (#312). A client that cancels such a
// consumer afterwards gets cancel-ok, as with RabbitMQ, not a channel error,
// and a queue declared again under the same name delivers to its new
// consumer.
func TestDeleteQueueWithConsumer_CancelAndRedeclare(t *testing.T) {
	_, _, addr := startTestBroker(t, nil, nil)
	conn, _ := dialTap(t, addr, testHeartbeat)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := ch.QueueDeclare("delq", false, false, false, false, nil); err != nil {
		t.Fatalf("queue.declare: %v", err)
	}
	if _, err := ch.Consume("delq", "old", true, false, false, false, nil); err != nil {
		t.Fatalf("basic.consume: %v", err)
	}
	if _, err := ch.QueueDelete("delq", false, false, false); err != nil {
		t.Fatalf("queue.delete: %v", err)
	}
	if err := ch.Cancel("old", false); err != nil {
		t.Fatalf("basic.cancel for the deleted queue's consumer: %v", err)
	}

	q, err := ch.QueueDeclare("delq", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("queue.declare again (channel closed by the cancel?): %v", err)
	}
	if q.Consumers != 0 {
		t.Errorf("redeclared queue reports %d consumers, want 0", q.Consumers)
	}
	deliveries, err := ch.Consume("delq", "new", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("basic.consume new: %v", err)
	}
	const n = 20
	for i := 0; i < n; i++ {
		if err := ch.Publish("", "delq", false, false, amqp091.Publishing{Body: []byte("m")}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	timeout := time.After(5 * time.Second)
	for got := 0; got < n; got++ {
		select {
		case d := <-deliveries:
			if d.ConsumerTag != "new" {
				t.Fatalf("delivery for consumer %q, want new", d.ConsumerTag)
			}
		case <-timeout:
			t.Fatalf("new consumer got %d of %d messages", got, n)
		}
	}
}
