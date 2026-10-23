package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Regression test for #324: another connection's basic.consume, basic.get
// and queue.declare (passive or not) on an exclusive queue close its channel
// with 405 RESOURCE_LOCKED; the owner can still do all of them.
func TestExclusiveQueue_OtherConnectionsGetResourceLocked(t *testing.T) {
	queueName := fmt.Sprintf("e2e.excl.locked.%d", time.Now().UnixNano())

	ownerConn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("failed to connect owner: %v", err)
	}
	defer ownerConn.Close()
	ownerCh, err := ownerConn.Channel()
	if err != nil {
		t.Fatalf("failed to open owner channel: %v", err)
	}
	if _, err := ownerCh.QueueDeclare(queueName, false, false, true, false, nil); err != nil {
		t.Fatalf("failed to declare exclusive queue: %v", err)
	}
	if err := ownerCh.Publish("", queueName, false, false, amqp.Publishing{Body: []byte("x")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	foreignConn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("failed to connect foreign: %v", err)
	}
	defer foreignConn.Close()

	attempts := map[string]func(ch *amqp.Channel) error{
		"basic.consume": func(ch *amqp.Channel) error {
			_, err := ch.Consume(queueName, "", true, false, false, false, nil)
			return err
		},
		"basic.get": func(ch *amqp.Channel) error {
			_, _, err := ch.Get(queueName, true)
			return err
		},
		"queue.declare": func(ch *amqp.Channel) error {
			_, err := ch.QueueDeclare(queueName, false, false, true, false, nil)
			return err
		},
		"queue.declare passive": func(ch *amqp.Channel) error {
			_, err := ch.QueueDeclarePassive(queueName, false, false, true, false, nil)
			return err
		},
	}
	for name, attempt := range attempts {
		t.Run("foreign "+name, func(t *testing.T) {
			ch, err := foreignConn.Channel()
			if err != nil {
				t.Fatalf("failed to open foreign channel: %v", err)
			}
			defer ch.Close()
			err = attempt(ch)
			amqpErr, ok := err.(*amqp.Error)
			if !ok {
				t.Fatalf("expected an AMQP error, got %T %v", err, err)
			}
			if amqpErr.Code != amqp.ResourceLocked {
				t.Errorf("got code %d (%s), want 405 RESOURCE_LOCKED", amqpErr.Code, amqpErr.Reason)
			}
			if !strings.Contains(amqpErr.Reason, "cannot obtain exclusive access to locked queue") {
				t.Errorf("unexpected reason %q", amqpErr.Reason)
			}
			if !ch.IsClosed() {
				t.Error("foreign channel still open")
			}
		})
	}

	// The refused get left the message for the owner.
	if _, ok, err := ownerCh.Get(queueName, true); err != nil || !ok {
		t.Fatalf("owner basic.get: ok=%v err=%v", ok, err)
	}
	if _, err := ownerCh.QueueDeclarePassive(queueName, false, false, true, false, nil); err != nil {
		t.Fatalf("owner passive declare: %v", err)
	}
	if _, err := ownerCh.QueueDeclare(queueName, false, false, true, false, nil); err != nil {
		t.Fatalf("owner declare: %v", err)
	}
	if _, err := ownerCh.Consume(queueName, "", true, false, false, false, nil); err != nil {
		t.Fatalf("owner consume: %v", err)
	}
	if foreignConn.IsClosed() {
		t.Fatal("foreign connection closed; the refusals should be channel exceptions")
	}
}
