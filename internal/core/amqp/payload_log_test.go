package amqp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureTraceLog sends everything logged during the test, at trace level and
// above, to the returned buffer.
func captureTraceLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = logger
		zerolog.SetGlobalLevel(level)
	})
	return &buf
}

// logLines decodes each JSON log line in buf.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		lines = append(lines, line)
	}
	return lines
}

// assertNoRawPayload fails if any log line holds raw in hex, base64 or as
// raw bytes.
func assertNoRawPayload(t *testing.T, buf *bytes.Buffer, raw []byte) {
	t.Helper()
	forms := []string{hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), string(raw)}
	for _, line := range logLines(t, buf) {
		for key, value := range line {
			s, ok := value.(string)
			if !ok {
				continue
			}
			for _, form := range forms {
				if strings.Contains(s, form) {
					t.Errorf("field %q holds the raw payload: %q", key, s)
				}
			}
		}
	}
}

// assertLogLine fails unless some log line has message msg and every field
// in want.
func assertLogLine(t *testing.T, buf *bytes.Buffer, msg string, want map[string]any) {
	t.Helper()
lines:
	for _, line := range logLines(t, buf) {
		if line["message"] != msg {
			continue
		}
		for key, value := range want {
			if line[key] != value {
				continue lines
			}
		}
		return
	}
	t.Errorf("no %q log line with fields %v in %q", msg, want, buf.String())
}

func methodPayload(classID, methodID uint16, args []byte) []byte {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.BigEndian, classID)
	_ = binary.Write(&payload, binary.BigEndian, methodID)
	payload.Write(args)
	return payload.Bytes()
}

func TestParseMethodFrame_LogsFieldsNotPayload(t *testing.T) {
	queueBind := func() []byte {
		var args bytes.Buffer
		_ = binary.Write(&args, binary.BigEndian, uint16(0))
		EncodeShortStr(&args, "orders")
		EncodeShortStr(&args, "amq.topic")
		EncodeShortStr(&args, "orders.created")
		args.WriteByte(0)
		EncodeLongStr(&args, requireEncoded(t)(EncodeTable(map[string]any{"x-match": "all"})))
		return args.Bytes()
	}
	channelClose := func() []byte {
		var args bytes.Buffer
		_ = binary.Write(&args, binary.BigEndian, uint16(200))
		EncodeShortStr(&args, "bye now")
		_ = binary.Write(&args, binary.BigEndian, uint16(0))
		_ = binary.Write(&args, binary.BigEndian, uint16(0))
		return args.Bytes()
	}
	queueDelete := func() []byte {
		var args bytes.Buffer
		_ = binary.Write(&args, binary.BigEndian, uint16(0))
		EncodeShortStr(&args, "orders")
		args.WriteByte(0x01) // if-unused
		return args.Bytes()
	}
	exchangeDelete := func() []byte {
		var args bytes.Buffer
		_ = binary.Write(&args, binary.BigEndian, uint16(0))
		EncodeShortStr(&args, "audit")
		args.WriteByte(0x02) // no-wait
		return args.Bytes()
	}

	tests := []struct {
		name    string
		payload []byte
		msg     string
		want    map[string]any
	}{
		{
			name:    "queue.bind",
			payload: methodPayload(uint16(QUEUE), uint16(QUEUE_BIND), queueBind()),
			msg:     "Received QUEUE_BIND frame",
			want:    map[string]any{"queue": "orders", "exchange": "amq.topic", "routing_key": "orders.created", "no_wait": false},
		},
		{
			name:    "channel.close",
			payload: methodPayload(uint16(CHANNEL), uint16(CHANNEL_CLOSE), channelClose()),
			msg:     "Received CHANNEL_CLOSE frame",
			want:    map[string]any{"reply_code": float64(200), "reply_text": "bye now"},
		},
		{
			name:    "queue.delete",
			payload: methodPayload(uint16(QUEUE), uint16(QUEUE_DELETE), queueDelete()),
			msg:     "Received QUEUE_DELETE frame",
			want:    map[string]any{"queue": "orders", "if_unused": true, "if_empty": false, "no_wait": false},
		},
		{
			name:    "exchange.delete",
			payload: methodPayload(uint16(EXCHANGE), uint16(EXCHANGE_DELETE), exchangeDelete()),
			msg:     "Received EXCHANGE_DELETE frame",
			want:    map[string]any{"exchange": "audit", "if_unused": false, "no_wait": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureTraceLog(t)
			if _, err := parseMethodFrame(1, tt.payload); err != nil {
				t.Fatalf("parseMethodFrame: %v", err)
			}
			assertLogLine(t, buf, tt.msg, tt.want)
			assertNoRawPayload(t, buf, tt.payload[4:])
		})
	}
}

func TestParseFrame_UnknownFrameLogsSizeNotFrame(t *testing.T) {
	buf := captureTraceLog(t)
	frame := []byte{99, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 'a', 'b', 'c', 'd', FRAME_END}

	if _, err := parseFrame(frame); err == nil {
		t.Fatal("expected an error for an unknown frame type")
	}

	assertLogLine(t, buf, "Received unknown frame", map[string]any{
		"frame_type": float64(99), "channel": float64(3), "frame_size": float64(len(frame)),
	})
	assertNoRawPayload(t, buf, frame)
	assertNoRawPayload(t, buf, frame[7:len(frame)-1])
}

func TestSendFrame_LogsHeaderNotFrame(t *testing.T) {
	buf := captureTraceLog(t)
	frame := formatMethodFrame(2, QUEUE, TypeMethod(QUEUE_DECLARE_OK), []byte("orders-queue-payload"))
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = io.Copy(io.Discard, client) }()

	if err := sendFrame(server, frame); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	assertLogLine(t, buf, "Sending frame", map[string]any{
		"frame_type": float64(TYPE_METHOD), "channel": float64(2), "frame_size": float64(len(frame)),
		"class_id": float64(QUEUE), "method_id": float64(QUEUE_DECLARE_OK),
	})
	assertNoRawPayload(t, buf, frame[7:len(frame)-1])
}

func TestCreateConnectionStartFrame_LogsSizeNotFrame(t *testing.T) {
	buf := captureTraceLog(t)
	cfg := HandshakeConfig{
		Protocol:         "AMQP 0-9-1",
		Mechanisms:       []string{"PLAIN"},
		Locales:          []string{"en_US"},
		ServerProperties: map[string]any{"product": "OtterMQ"},
	}

	frame, err := createConnectionStartFrame(cfg)
	if err != nil {
		t.Fatalf("createConnectionStartFrame: %v", err)
	}

	assertLogLine(t, buf, "Sending CONNECTION_START frame", map[string]any{"frame_size": float64(len(frame))})
	if strings.Contains(buf.String(), fmt.Sprintf("%v", frame)) || strings.Contains(buf.String(), "OtterMQ") {
		t.Errorf("the frame was logged: %q", buf.String())
	}
}

// The broker logs channel states while it assembles a message; the body must
// not reach the log.
func TestChannelState_LogsSizesNotBody(t *testing.T) {
	buf := captureTraceLog(t)
	body := []byte("message body that must stay out of the log")
	var state any = &ChannelState{
		MethodFrame: &RequestMethodMessage{Channel: 4, ClassID: uint16(BASIC), MethodID: uint16(BASIC_PUBLISH)},
		HeaderFrame: &HeaderFrame{Channel: 4, ClassID: uint16(BASIC), BodySize: uint64(len(body))},
		Body:        body,
		BodySize:    uint64(len(body)),
	}
	marshaler, ok := state.(zerolog.LogObjectMarshaler)
	if !ok {
		t.Fatal("ChannelState doesn't implement zerolog.LogObjectMarshaler, so logging it dumps the body")
	}

	log.Trace().Object("state", marshaler).Msg("state")

	line := logLines(t, buf)[0]
	got, _ := line["state"].(map[string]any)
	want := map[string]any{
		"channel": float64(4), "class_id": float64(BASIC), "method_id": float64(BASIC_PUBLISH),
		"has_header": true, "body_size": float64(len(body)), "body_len": float64(len(body)),
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("state field %q = %v, want %v (line %v)", key, got[key], value, line)
		}
	}
	if strings.Contains(buf.String(), string(body)) || strings.Contains(buf.String(), base64.StdEncoding.EncodeToString(body)) {
		t.Errorf("the body was logged: %q", buf.String())
	}
}
