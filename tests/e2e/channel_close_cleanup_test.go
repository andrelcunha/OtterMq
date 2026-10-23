package e2e

// Regression tests for #219: closing a channel (from either side) or a
// connection must cancel its consumers, requeue its unacked deliveries
// (consumer and basic.get) as redelivered, and drop its per-channel state.

import (
	"context"
	"testing"
	"time"

	"github.com/ottermq/ottermq/internal/core/broker/vhost"
	amqp "github.com/rabbitmq/amqp091-go"
)

func publishBody(t *testing.T, ch *amqp.Channel, queue, body string) {
	t.Helper()
	err := ch.PublishWithContext(context.Background(), "", queue, false, false,
		amqp.Publishing{Body: []byte(body)})
	if err != nil {
		t.Fatalf("publish %q: %v", body, err)
	}
}

func readyCount(t *testing.T, ch *amqp.Channel, queue string) amqp.Queue {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	q, err := ch.QueueDeclarePassive(queue, false, false, false, false, nil)
	if err != nil {
		t.Fatalf("passive declare %s: %v", queue, err)
	}
	return q
}

// consumerCount reads the queue's consumers from the in-process broker
// (declare-ok always reports 0), or returns want when there is none.
func consumerCount(t *testing.T, queue string, want int) int {
	t.Helper()
	if testBroker == nil {
		return want
	}
	return len(testBroker.GetVHost("/").GetActiveConsumersForQueue(queue))
}

func expectOpen(t *testing.T, closed <-chan *amqp.Error, what string) {
	t.Helper()
	select {
	case e := <-closed:
		t.Fatalf("%s: connection closed: %v", what, e)
	case <-time.After(500 * time.Millisecond):
	}
}

// assertNoChannelState checks, when the broker runs in-process, that the
// vhost keeps no delivery or transaction state for the given channels.
func assertNoChannelState(t *testing.T, c *amqp.Connection, channels ...uint16) {
	t.Helper()
	assertNoChannelStateFor(t, c.LocalAddr().String(), channels...)
}

// assertNoChannelStateFor takes the client's local address, which is the
// broker's connection ID.
func assertNoChannelStateFor(t *testing.T, addr string, channels ...uint16) {
	t.Helper()
	if testBroker == nil {
		return
	}
	vh := testBroker.GetVHost("/")
	connID := vhost.ConnectionID(addr)
	for _, n := range channels {
		if st := vh.GetChannelDelivery(vhost.ConnectionChannelKey{ConnectionID: connID, Channel: n}); st != nil {
			t.Errorf("channel %d: delivery state left behind", n)
		}
		if tx := vh.GetTransactionState(n, connID); tx != nil {
			t.Errorf("channel %d: transaction state left behind", n)
		}
	}
}

// The reproduction from #219: connection 1 stays open, and "a" is
// redelivered to another consumer.
func TestChannelClose_RequeuesConsumerUnacked(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}

	c1, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c1.Close()
	closed := c1.NotifyClose(make(chan *amqp.Error, 1))
	ch1, err := c1.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	msgs1, err := ch1.Consume(q, "d1tag", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	publishBody(t, obs.Ch, q, "a")
	if d, ok := obs.ConsumeWithTimeout(msgs1, 2*time.Second); !ok || string(d.Body) != "a" {
		t.Fatalf("first delivery: ok=%v body=%q", ok, d.Body)
	}
	if err := ch1.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}

	st := readyCount(t, obs.Ch, q)
	if n := consumerCount(t, q, 0); st.Messages != 1 || n != 0 {
		t.Errorf("after channel.close: %d ready, %d consumers; want 1, 0", st.Messages, n)
	}
	assertNoChannelState(t, c1, 1)

	other := NewTestConnection(t, brokerURL)
	defer other.Close()
	msgs2 := other.StartConsumer(q, "other", false)
	d, ok := other.ConsumeWithTimeout(msgs2, 2*time.Second)
	if !ok || string(d.Body) != "a" || !d.Redelivered {
		t.Fatalf("redelivery to the other consumer: ok=%v body=%q redelivered=%v", ok, d.Body, d.Redelivered)
	}
	d.Ack(false)

	publishBody(t, other.Ch, q, "b")
	d, ok = other.ConsumeWithTimeout(msgs2, 2*time.Second)
	if !ok || string(d.Body) != "b" {
		t.Fatalf("\"b\" not delivered to the open consumer: ok=%v body=%q", ok, d.Body)
	}
	d.Ack(false)
	expectOpen(t, closed, "after a publish to the closed channel's queue")
}

// Requeued deliveries keep their order (#219 asks for delivery-tag order).
func TestChannelClose_RequeuesInDeliveryOrder(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	obs.PublishMessages(q, 5)

	c1 := NewTestConnection(t, brokerURL)
	defer c1.Close()
	// One basic.get delivery between consumer deliveries.
	if _, ok, err := c1.Ch.Get(q, false); err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	msgs := c1.StartConsumer(q, "first", false)
	c1.WaitForMessages(msgs, 4, 2*time.Second)
	if err := c1.Ch.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}
	c1.Ch = nil

	other := NewTestConnection(t, brokerURL)
	defer other.Close()
	got := other.WaitForMessages(other.StartConsumer(q, "second", false), 5, 3*time.Second)
	for i, d := range got {
		want := "Message " + string(rune('0'+i))
		if string(d.Body) != want || !d.Redelivered {
			t.Errorf("delivery %d: body=%q redelivered=%v; want %q redelivered", i, d.Body, d.Redelivered, want)
		}
	}
	other.AckAll(got)
}

func TestChannelClose_RequeuesUnackedGet(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	obs.PublishMessages(q, 1)

	c1 := NewTestConnection(t, brokerURL)
	defer c1.Close()
	if _, ok, err := c1.Ch.Get(q, false); err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if err := c1.Ch.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}
	c1.Ch = nil

	if st := readyCount(t, obs.Ch, q); st.Messages != 1 {
		t.Fatalf("after channel.close: %d ready, want 1 (unacked basic.get requeued)", st.Messages)
	}
	assertNoChannelState(t, c1.Conn, 1)
	d, ok, err := obs.Ch.Get(q, true)
	if err != nil || !ok || !d.Redelivered {
		t.Fatalf("get after requeue: ok=%v err=%v redelivered=%v", ok, err, d.Redelivered)
	}
}

func TestConnectionClose_RequeuesUnackedGet(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	obs.PublishMessages(q, 2)

	c1 := NewTestConnection(t, brokerURL)
	if _, ok, err := c1.Ch.Get(q, false); err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	// A second channel with a transaction and a basic.get delivery.
	ch2, err := c1.Conn.Channel()
	if err != nil {
		t.Fatalf("channel 2: %v", err)
	}
	if err := ch2.Tx(); err != nil {
		t.Fatalf("tx.select: %v", err)
	}
	if _, ok, err := ch2.Get(q, false); err != nil || !ok {
		t.Fatalf("get on channel 2: ok=%v err=%v", ok, err)
	}
	c1.Conn.Close()
	c1.Ch, c1.Conn = nil, nil

	if st := readyCount(t, obs.Ch, q); st.Messages != 2 {
		t.Fatalf("after connection close: %d ready, want 2 (unacked basic.get requeued)", st.Messages)
	}
	d, ok, err := obs.Ch.Get(q, true)
	if err != nil || !ok || !d.Redelivered {
		t.Fatalf("get after requeue: ok=%v err=%v redelivered=%v", ok, err, d.Redelivered)
	}
}

// The connection-level check: nothing is left in the vhost for any channel
// of a closed connection.
func TestConnectionClose_LeavesNoChannelState(t *testing.T) {
	if testBroker == nil {
		t.Skip("needs the in-process broker")
	}
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	obs.PublishMessages(q, 1)

	c1, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ch1, _ := c1.Channel()
	ch2, _ := c1.Channel()
	if err := ch1.Qos(3, 0, false); err != nil {
		t.Fatalf("qos: %v", err)
	}
	if err := ch2.Tx(); err != nil {
		t.Fatalf("tx.select: %v", err)
	}
	if _, ok, err := ch1.Get(q, false); err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	addr := c1.LocalAddr().String()
	c1.Close()
	time.Sleep(300 * time.Millisecond)
	assertNoChannelStateFor(t, addr, 1, 2)
}

// A channel the broker closes (here with 404 on a passive declare) is cleaned
// up too: its consumer's unacked delivery is requeued, and nothing is sent on
// it afterwards.
func TestChannelClose_ServerInitiated_RequeuesUnacked(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}

	c1 := NewTestConnection(t, brokerURL)
	defer c1.Close()
	closed := c1.Conn.NotifyClose(make(chan *amqp.Error, 1))
	msgs := c1.StartConsumer(q, "victim", false)
	publishBody(t, obs.Ch, q, "a")
	if _, ok := c1.ConsumeWithTimeout(msgs, 2*time.Second); !ok {
		t.Fatal("no delivery")
	}
	chClosed := c1.Ch.NotifyClose(make(chan *amqp.Error, 1))
	if _, err := c1.Ch.QueueDeclarePassive(q+"-missing", false, false, false, false, nil); err == nil {
		t.Fatal("passive declare of a missing queue succeeded")
	}
	select {
	case <-chClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed by the broker")
	}
	c1.Ch = nil

	st := readyCount(t, obs.Ch, q)
	if n := consumerCount(t, q, 0); st.Messages != 1 || n != 0 {
		t.Errorf("after server channel.close: %d ready, %d consumers; want 1, 0", st.Messages, n)
	}
	assertNoChannelState(t, c1.Conn, 1)
	publishBody(t, obs.Ch, q, "b")
	expectOpen(t, closed, "after a publish to the closed channel's queue")
	d, ok, err := obs.Ch.Get(q, true)
	if err != nil || !ok || string(d.Body) != "a" || !d.Redelivered {
		t.Fatalf("requeued delivery: ok=%v err=%v body=%q redelivered=%v", ok, err, d.Body, d.Redelivered)
	}
}

// Reopening a channel number starts clean: delivery tag 1, no prefetch, flow
// active, no transaction.
func TestChannelReopen_StartsClean(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	q := obs.UniqueQueueName("cc219")
	if _, err := obs.Ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}
	publishBody(t, obs.Ch, q, "old")

	// channel-max 1: the client can only ever use channel 1.
	c, err := amqp.DialConfig(brokerURL, amqp.Config{ChannelMax: 1})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	ch, err := c.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, ok, err := ch.Get(q, false); err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if err := ch.Qos(1, 0, false); err != nil {
		t.Fatalf("qos: %v", err)
	}
	if err := ch.Flow(false); err != nil {
		t.Fatalf("flow: %v", err)
	}
	if err := ch.Tx(); err != nil {
		t.Fatalf("tx.select: %v", err)
	}
	publishBody(t, ch, q, "uncommitted")
	if err := ch.Close(); err != nil {
		t.Fatalf("channel close: %v", err)
	}

	ch, err = c.Channel()
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ch.Close()
	// Not in a transaction: these are routed at once.
	publishBody(t, ch, q, "n1")
	publishBody(t, ch, q, "n2")
	if st := readyCount(t, obs.Ch, q); st.Messages != 3 {
		t.Fatalf("after reopen: %d ready, want 3 (old requeued, n1, n2)", st.Messages)
	}
	msgs, err := ch.Consume(q, "", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	// No prefetch limit and flow active: all three arrive without acks.
	var got []amqp.Delivery
	timeout := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case d := <-msgs:
			got = append(got, d)
		case <-timeout:
			t.Fatalf("got %d deliveries without acking, want 3 (stale prefetch or flow?)", len(got))
		}
	}
	for i, d := range got {
		if d.DeliveryTag != uint64(i+1) {
			t.Errorf("delivery %d (%q): tag %d, want %d", i, d.Body, d.DeliveryTag, i+1)
		}
		if string(d.Body) == "uncommitted" {
			t.Errorf("the closed channel's uncommitted publish was routed")
		}
	}
	if err := got[2].Ack(true); err != nil {
		t.Fatalf("ack: %v", err)
	}
}

// #220 is still open (consumer keys are channel number + tag, not
// connection-scoped). Closing a channel must not cancel another connection's
// consumer that uses the same channel number and tag.
func TestChannelClose_KeepsOtherConnectionsConsumer(t *testing.T) {
	obs := NewTestConnection(t, brokerURL)
	defer obs.Close()
	qB := obs.UniqueQueueName("cc219-b")
	qA := obs.UniqueQueueName("cc219-a")
	for _, name := range []string{qA, qB} {
		if _, err := obs.Ch.QueueDeclare(name, false, false, false, false, nil); err != nil {
			t.Fatalf("declare: %v", err)
		}
	}

	connB := NewTestConnection(t, brokerURL) // its channel 1
	defer connB.Close()
	msgsB := connB.StartConsumer(qB, "shared", false)

	connA := NewTestConnection(t, brokerURL) // also channel 1
	defer connA.Close()
	// Same channel number and tag: refused with 530/406 because of #220,
	// which closes A's channel 1 from the broker side.
	chClosed := connA.Ch.NotifyClose(make(chan *amqp.Error, 1))
	if _, err := connA.Ch.Consume(qA, "shared", false, false, false, false, nil); err == nil {
		t.Log("consume with a shared tag accepted (#220 fixed?)")
		if err := connA.Ch.Close(); err != nil {
			t.Fatalf("channel close: %v", err)
		}
	} else {
		<-chClosed
	}
	connA.Ch = nil
	// A client-initiated close of another channel 1 with no consumer.
	chA, err := connA.Conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	chA.Close()

	publishBody(t, obs.Ch, qB, "for-b")
	d, ok := connB.ConsumeWithTimeout(msgsB, 2*time.Second)
	if !ok || string(d.Body) != "for-b" {
		t.Fatalf("connection B's consumer lost: ok=%v body=%q", ok, d.Body)
	}
	d.Ack(false)
	if n := consumerCount(t, qB, 1); n != 1 {
		t.Errorf("queue B has %d consumers, want 1", n)
	}
}
