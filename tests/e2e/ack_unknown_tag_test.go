package e2e

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// waitForChannelException waits for the broker to close ch and returns the
// exception it was closed with.
func waitForChannelException(t *testing.T, closes <-chan *amqp.Error) *amqp.Error {
	t.Helper()
	select {
	case amqpErr, ok := <-closes:
		if !ok {
			t.Fatal("channel close notification channel was closed without an error")
		}
		if amqpErr == nil {
			t.Fatal("channel closed without an error")
		}
		return amqpErr
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the channel to be closed")
		return nil
	}
}

// TestUnknownDeliveryTagClosesChannel verifies that basic.ack/nack/reject of a
// delivery tag the channel never received closes that channel with 406
// PRECONDITION_FAILED (not the connection), per issue #226.
func TestUnknownDeliveryTagClosesChannel(t *testing.T) {
	conn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	t.Run("ack on a channel with no deliveries", func(t *testing.T) {
		ch, err := conn.Channel()
		if err != nil {
			t.Fatalf("failed to open channel: %v", err)
		}
		closes := ch.NotifyClose(make(chan *amqp.Error, 1))

		if err := ch.Ack(5, false); err != nil {
			t.Fatalf("Ack returned a client-side error: %v", err)
		}

		amqpErr := waitForChannelException(t, closes)
		if amqpErr.Code != amqp.PreconditionFailed {
			t.Fatalf("expected reply code %d, got %d (%s)", amqp.PreconditionFailed, amqpErr.Code, amqpErr.Reason)
		}
	})

	t.Run("double ack", func(t *testing.T) {
		ch, err := conn.Channel()
		if err != nil {
			t.Fatalf("failed to open channel: %v", err)
		}
		closes := ch.NotifyClose(make(chan *amqp.Error, 1))

		queue, err := ch.QueueDeclare("", false, true, true, false, nil)
		if err != nil {
			t.Fatalf("failed to declare queue: %v", err)
		}
		if err := ch.PublishWithContext(context.Background(), "", queue.Name, false, false, amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte("double-ack"),
		}); err != nil {
			t.Fatalf("failed to publish: %v", err)
		}

		var delivery amqp.Delivery
		deadline := time.Now().Add(5 * time.Second)
		for {
			var ok bool
			delivery, ok, err = ch.Get(queue.Name, false)
			if err != nil {
				t.Fatalf("failed to get message: %v", err)
			}
			if ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for the delivery")
			}
			time.Sleep(50 * time.Millisecond)
		}

		if err := delivery.Ack(false); err != nil {
			t.Fatalf("first ack failed: %v", err)
		}
		if err := delivery.Ack(false); err != nil {
			t.Fatalf("second ack returned a client-side error: %v", err)
		}

		amqpErr := waitForChannelException(t, closes)
		if amqpErr.Code != amqp.PreconditionFailed {
			t.Fatalf("expected reply code %d, got %d (%s)", amqp.PreconditionFailed, amqpErr.Code, amqpErr.Reason)
		}
	})

	// The channel exceptions above must not have taken the connection down.
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("connection unusable after channel exception: %v", err)
	}
	_ = ch.Close()
}
