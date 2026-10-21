package e2e

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Regression test for #230: deleting a durable queue bound to a topic
// exchange left the binding behind, so a persistent publish matching it was
// routed to the deleted queue, failed to persist, and the broker closed the
// publisher's connection with 541.
func TestQueueDeleteRemovesTopicBindings(t *testing.T) {
	conn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))

	const queue, pattern = "e2e-230-topic-q", "e2e230.#"
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatalf("QueueDeclare: %v", err)
	}
	if err := ch.QueueBind(queue, pattern, "amq.topic", false, nil); err != nil {
		t.Fatalf("QueueBind: %v", err)
	}
	if _, err := ch.QueueDelete(queue, false, false, false); err != nil {
		t.Fatalf("QueueDelete: %v", err)
	}

	err = ch.PublishWithContext(context.Background(), "amq.topic", "e2e230.a", true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		Body:         []byte("after delete"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// The only matching queue is gone, so the mandatory publish comes back.
	select {
	case ret := <-returns:
		if ret.ReplyCode != amqp.NoRoute {
			t.Fatalf("basic.return reply code %d (%s), want %d NO_ROUTE", ret.ReplyCode, ret.ReplyText, amqp.NoRoute)
		}
	case amqpErr := <-connClosed:
		t.Fatalf("broker closed the connection instead of returning the message: %v", amqpErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for basic.return")
	}

	// And the connection is still usable.
	if _, err := ch.QueueDeclarePassive("amq.gen-does-not-matter", false, false, false, false, nil); err == nil {
		t.Fatal("expected passive declare of a missing queue to fail with 404")
	}
	if conn.IsClosed() {
		t.Fatal("connection closed after publishing to a deleted queue's pattern")
	}
}
