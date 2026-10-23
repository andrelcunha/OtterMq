package e2e

// Regression tests for #299: basic.cancel only stops new deliveries. The
// deliveries already sent stay unacked on the channel, can be acked, nacked
// or rejected, and are requeued on channel close, connection close or
// basic.recover.

import (
	"fmt"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// cancelSetup declares a queue, publishes n messages, consumes all n
// unacked with prefetch n on a fresh connection and cancels the consumer.
func cancelSetup(t *testing.T, n int, tag string) (obs, c *TestConnection, q string, got []amqp.Delivery) {
	t.Helper()
	obs = NewTestConnection(t, brokerURL)
	q = obs.UniqueQueueName("cancel299")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	obs.PublishMessages(q, n)

	c = NewTestConnection(t, brokerURL)
	c.SetQoS(n, false)
	msgs := c.StartConsumer(q, tag, false)
	got = c.WaitForMessages(msgs, n, 3*time.Second)
	if err := c.Ch.Cancel(tag, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if st := readyCount(t, obs.Ch, q); st.Messages != 0 {
		t.Fatalf("after basic.cancel: %d ready, want 0 (deliveries stay unacked)", st.Messages)
	}
	return obs, c, q, got
}

// The repro from #299: prefetch 5, consume 5, cancel, ack all 5.
func TestCancel_AckAfterCancel(t *testing.T) {
	obs, c, q, got := cancelSetup(t, 5, "graceful")
	defer obs.Close()
	defer c.Close()
	closed := c.Ch.NotifyClose(make(chan *amqp.Error, 1))
	c.AckAll(got)
	select {
	case e := <-closed:
		t.Fatalf("channel closed by the acks after cancel: %v", e)
	case <-time.After(300 * time.Millisecond):
	}
	if err := c.Ch.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}
	c.Ch = nil
	if st := readyCount(t, obs.Ch, q); st.Messages != 0 {
		t.Errorf("queue has %d messages after acking all and closing, want 0", st.Messages)
	}
}

func TestCancel_NackAndRejectAfterCancel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		settle  func(d amqp.Delivery) error
		requeue bool
	}{
		{"nack requeue", func(d amqp.Delivery) error { return d.Nack(false, true) }, true},
		{"nack drop", func(d amqp.Delivery) error { return d.Nack(false, false) }, false},
		{"reject requeue", func(d amqp.Delivery) error { return d.Reject(true) }, true},
		{"reject drop", func(d amqp.Delivery) error { return d.Reject(false) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, c, q, got := cancelSetup(t, 2, "settle")
			defer obs.Close()
			defer c.Close()
			for _, d := range got {
				if err := tc.settle(d); err != nil {
					t.Fatalf("settle: %v", err)
				}
			}
			want := 0
			if tc.requeue {
				want = 2
			}
			if st := readyCount(t, obs.Ch, q); st.Messages != want {
				t.Fatalf("after settling: %d ready, want %d", st.Messages, want)
			}
			// Closing the channel requeues nothing more: no duplicates.
			if err := c.Ch.Close(); err != nil {
				t.Fatalf("channel close (was it closed by the broker?): %v", err)
			}
			c.Ch = nil
			if st := readyCount(t, obs.Ch, q); st.Messages != want {
				t.Fatalf("after channel close: %d ready, want %d", st.Messages, want)
			}
			for i := 0; i < want; i++ {
				d, ok, err := obs.Ch.Get(q, true)
				if err != nil || !ok || !d.Redelivered {
					t.Errorf("requeued copy %d: ok=%v err=%v redelivered=%v", i, ok, err, d.Redelivered)
				}
			}
		})
	}
}

func TestCancel_RecoverAfterCancel(t *testing.T) {
	for _, requeue := range []bool{true, false} {
		t.Run(fmt.Sprintf("requeue=%v", requeue), func(t *testing.T) {
			obs, c, q, _ := cancelSetup(t, 3, "recover")
			defer obs.Close()
			defer c.Close()
			if err := c.Ch.Recover(requeue); err != nil {
				t.Fatalf("recover: %v", err)
			}
			// No consumer to redeliver to: back in the queue either way.
			if st := readyCount(t, obs.Ch, q); st.Messages != 3 {
				t.Fatalf("after basic.recover: %d ready, want 3", st.Messages)
			}
			for i := 0; i < 3; i++ {
				d, ok, err := obs.Ch.Get(q, true)
				if err != nil || !ok || !d.Redelivered {
					t.Errorf("recovered copy %d: ok=%v err=%v redelivered=%v", i, ok, err, d.Redelivered)
				}
			}
		})
	}
}

func TestCancel_ChannelCloseRequeues(t *testing.T) {
	obs, c, q, _ := cancelSetup(t, 3, "close-ch")
	defer obs.Close()
	defer c.Close()
	if err := c.Ch.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}
	c.Ch = nil
	if st := readyCount(t, obs.Ch, q); st.Messages != 3 {
		t.Fatalf("after channel close: %d ready, want 3", st.Messages)
	}
	for i := 0; i < 3; i++ {
		d, ok, err := obs.Ch.Get(q, true)
		want := fmt.Sprintf("Message %d", i)
		if err != nil || !ok || !d.Redelivered || string(d.Body) != want {
			t.Errorf("requeued %d: ok=%v err=%v body=%q redelivered=%v, want %q", i, ok, err, d.Body, d.Redelivered, want)
		}
	}
}

func TestCancel_ConnectionCloseRequeues(t *testing.T) {
	obs, c, q, _ := cancelSetup(t, 3, "close-conn")
	defer obs.Close()
	c.Close()
	if st := readyCount(t, obs.Ch, q); st.Messages != 3 {
		t.Fatalf("after connection close: %d ready, want 3", st.Messages)
	}
}

// A new consumer reusing the cancelled tag on the same channel doesn't
// inherit the old deliveries: they don't take up its prefetch, and the old
// ones can still be acked.
func TestCancel_TagReuseStartsWithFreshPrefetch(t *testing.T) {
	obs, c, q, old := cancelSetup(t, 3, "reused")
	defer obs.Close()
	defer c.Close()
	msgs := c.StartConsumer(q, "reused", false) // prefetch 3 again
	obs.PublishMessages(q, 3)
	fresh := c.WaitForMessages(msgs, 3, 3*time.Second)
	for _, d := range fresh {
		if d.Redelivered {
			t.Errorf("new consumer got a redelivery %q: the cancel requeued the old deliveries", d.Body)
		}
	}
	c.AckAll(old)
	c.AckAll(fresh)
	if err := c.Ch.Cancel("reused", false); err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	if err := c.Ch.Close(); err != nil {
		t.Fatalf("channel close (was it closed by an ack?): %v", err)
	}
	c.Ch = nil
	if st := readyCount(t, obs.Ch, q); st.Messages != 0 {
		t.Errorf("queue has %d messages, want 0", st.Messages)
	}
}
