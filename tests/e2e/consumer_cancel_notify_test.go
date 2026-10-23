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
