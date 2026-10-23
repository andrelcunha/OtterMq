package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Deleting a queue from another channel cancels its consumer, and the
// client, which advertises consumer_cancel_notify (amqp091-go always does),
// is told with basic.cancel (#320).
func TestQueueDeleteNotifiesConsumerCancel(t *testing.T) {
	tc := NewTestConnection(t, brokerURL)
	defer tc.Close()

	queue := tc.UniqueQueueName("cancel-notify")
	_, err := tc.Ch.QueueDeclare(queue, false, false, false, false, nil)
	require.NoError(t, err)

	tag := tc.UniqueConsumerTag("notified")
	cancels := tc.Ch.NotifyCancel(make(chan string, 1))
	msgs := tc.StartConsumer(queue, tag, true)

	deleter, err := tc.Conn.Channel()
	require.NoError(t, err)
	defer deleter.Close()
	_, err = deleter.QueueDelete(queue, false, false, false)
	require.NoError(t, err)

	select {
	case got := <-cancels:
		require.Equal(t, tag, got, "basic.cancel for the wrong consumer")
	case <-time.After(5 * time.Second):
		t.Fatal("no basic.cancel within 5s of deleting the queue")
	}

	// amqp091-go closes the consumer's delivery channel on basic.cancel.
	select {
	case _, open := <-msgs:
		require.False(t, open, "unexpected delivery after the queue was deleted")
	case <-time.After(5 * time.Second):
		t.Fatal("the consumer's delivery channel was not closed")
	}

	// The channel stays usable: the cancel is no channel error.
	_, err = tc.Ch.QueueDeclare(tc.UniqueQueueName("after-cancel"), false, true, false, false, nil)
	require.NoError(t, err, "the consumer's channel is unusable after basic.cancel")
}

// A consumer that deletes its own queue gets basic.cancel before
// queue.delete-ok, so reusing its tag right away for a new consumer isn't
// undone by a late cancel (#320).
func TestQueueDeleteCancelPrecedesDeleteOk(t *testing.T) {
	tc := NewTestConnection(t, brokerURL)
	defer tc.Close()

	tag := tc.UniqueConsumerTag("reused")
	cancels := tc.Ch.NotifyCancel(make(chan string, 1))
	for round := 0; round < 20; round++ {
		queue := tc.UniqueQueueName("cancel-order")
		_, err := tc.Ch.QueueDeclare(queue, false, false, false, false, nil)
		require.NoError(t, err)
		msgs := tc.StartConsumer(queue, tag, true)

		if round > 0 {
			// The previous round's cancel arrived before this consume.
			select {
			case got := <-cancels:
				t.Fatalf("round %d: basic.cancel for %s arrived after the tag was reused", round, got)
			default:
			}
		}
		_, err = tc.Ch.QueueDelete(queue, false, false, false)
		require.NoError(t, err)
		select {
		case got := <-cancels:
			require.Equal(t, tag, got)
		default:
			t.Fatalf("round %d: no basic.cancel before queue.delete-ok", round)
		}
		_, open := <-msgs
		require.False(t, open, "round %d: delivery channel not closed", round)
	}

	// The tag is usable again and the consumer receives messages.
	queue := tc.UniqueQueueName("cancel-order-final")
	_, err := tc.Ch.QueueDeclare(queue, false, true, false, false, nil)
	require.NoError(t, err)
	msgs := tc.StartConsumer(queue, tag, true)
	tc.PublishMessages(queue, 1)
	_, ok := tc.ConsumeWithTimeout(msgs, 2*time.Second)
	require.True(t, ok, "the consumer reusing the tag got nothing")
}
