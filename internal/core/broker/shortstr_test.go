package broker

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/ottermq/ottermq/internal/core/amqp"
	amqperrors "github.com/ottermq/ottermq/internal/core/amqp/errors"
)

type shortStrRecordingConn struct {
	net.Conn
	written bytes.Buffer
}

func (c *shortStrRecordingConn) Write(data []byte) (int, error) {
	return c.written.Write(data)
}

func TestBasicReturnEncodingErrorWritesNoFrames(t *testing.T) {
	for _, tt := range []struct {
		name     string
		exchange string
		props    amqp.BasicProperties
	}{
		{name: "method", exchange: strings.Repeat("x", 256)},
		{name: "header", props: amqp.BasicProperties{CorrelationID: strings.Repeat("\u00e9", 128)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &Broker{framer: &amqp.DefaultFramer{}}
			conn := &shortStrRecordingConn{}
			_, err := b.BasicReturn(conn, 1, tt.exchange, "routing-key", &amqp.Message{Body: []byte("body"), Properties: tt.props})
			if err == nil || !strings.Contains(err.Error(), "255 bytes") {
				t.Fatalf("expected shortstr length error, got %v", err)
			}
			if conn.written.Len() != 0 {
				t.Errorf("encoding failure wrote %d bytes", conn.written.Len())
			}
		})
	}
}

type failingChannelCloseFramer struct {
	amqp.DefaultFramer
	err error
}

func (f *failingChannelCloseFramer) CreateChannelCloseFrame(_, _, _, _ uint16, _ string) ([]byte, error) {
	return nil, f.err
}

func TestChannelErrorResponsePropagatesEncodingError(t *testing.T) {
	b, _, conn := createTestBroker()
	wantErr := errors.New("synthetic close frame encoding failure")
	b.framer = &failingChannelCloseFramer{err: wantErr}
	request := &amqp.RequestMethodMessage{Channel: 1, ClassID: uint16(amqp.QUEUE), MethodID: uint16(amqp.QUEUE_DECLARE)}
	cause := amqperrors.NewChannelError(strings.Repeat("x", 256), uint16(amqp.NOT_FOUND), request.ClassID, request.MethodID)
	_, err := sendChannelErrorResponse(cause, b, conn, request)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected propagated frame encoding error %v, got %v", wantErr, err)
	}
}
