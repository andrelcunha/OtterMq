package e2e

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPassiveDeclareMaxLengthMissingQueueClosesOnlyChannel(t *testing.T) {
	conn, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	connectionCloses := conn.NotifyClose(make(chan *amqp.Error, 1))
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}

	name := "missing-181-" + strings.Repeat("q", 255-len("missing-181-"))
	_, err = ch.QueueDeclarePassive(name, false, false, false, false, nil)
	var channelError *amqp.Error
	if !errors.As(err, &channelError) || channelError.Code != amqp.NotFound || !channelError.Server {
		t.Logf("after passive declare: channel closed = %v, connection closed = %v", ch.IsClosed(), conn.IsClosed())
		t.Fatalf("passive declare error = %v, want server channel.close 404", err)
	}
	want := "NOT_FOUND - no queue '" + name + "' in vhost '/'"
	if channelError.Reason != want[:255] || !utf8.ValidString(channelError.Reason) {
		t.Fatalf("reply text = %q (%d bytes), want the first 255 UTF8 bytes", channelError.Reason, len(channelError.Reason))
	}
	if !ch.IsClosed() || conn.IsClosed() {
		t.Fatalf("closed channel = %v, closed connection = %v", ch.IsClosed(), conn.IsClosed())
	}

	// A fresh channel and a successful queue declaration prove that the same
	// connection still carries requests after the channel.close handshake.
	healthy, err := conn.Channel()
	if err != nil {
		t.Fatalf("connection unusable after channel exception: %v", err)
	}
	defer healthy.Close()
	if _, err := healthy.QueueDeclare("", false, true, true, false, nil); err != nil {
		t.Fatalf("queue declaration on surviving connection: %v", err)
	}
	select {
	case closeError := <-connectionCloses:
		t.Fatalf("connection unexpectedly closed: %v", closeError)
	default:
	}
}
