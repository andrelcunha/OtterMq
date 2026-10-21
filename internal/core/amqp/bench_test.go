package amqp

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// muteLogs disables logging for the rest of the benchmark: several hot-path
// functions log at debug level, which would otherwise dominate the numbers.
func muteLogs(b *testing.B) {
	b.Helper()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.Disabled)
	b.Cleanup(func() { zerolog.SetGlobalLevel(level) })
}

// readerConn is a net.Conn whose Read serves a fixed frame over and over.
// Only Read is implemented; readFrame uses nothing else.
type readerConn struct {
	net.Conn
	r bytes.Reader
}

func (c *readerConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// benchPayloadSizes are the body sizes from #241: empty, 1 KiB, 64 KiB, 1 MiB.
var benchPayloadSizes = []int{0, 1 << 10, 64 << 10, 1 << 20}

func sizeName(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%dMiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%dKiB", n>>10)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func benchPublishPayload() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0}) // reserved-1
	EncodeShortStr(&buf, "amq.direct")
	EncodeShortStr(&buf, "bench.routing.key")
	buf.WriteByte(0) // mandatory, immediate
	return buf.Bytes()
}

func benchMessage(bodySize int) Message {
	return Message{
		Body: make([]byte, bodySize),
		Properties: BasicProperties{
			ContentType:  "application/octet-stream",
			DeliveryMode: PERSISTENT,
			MessageID:    "msg-0001",
			Timestamp:    time.Unix(1700000000, 0),
			Headers:      map[string]any{"x-bench": "yes"},
		},
	}
}

func BenchmarkReadFrame(b *testing.B) {
	muteLogs(b)
	frames := []struct {
		name  string
		frame []byte
	}{
		{"method/basic.publish", formatMethodFrame(1, BASIC, TypeMethod(BASIC_PUBLISH), benchPublishPayload())},
		{"header", createHeaderFrame(1, uint16(BASIC), benchMessage(1024))},
	}
	for _, n := range benchPayloadSizes {
		frames = append(frames, struct {
			name  string
			frame []byte
		}{"body/" + sizeName(n), createBodyFrame(1, make([]byte, n))})
	}

	for _, f := range frames {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(f.frame)))
			conn := &readerConn{}
			for i := 0; i < b.N; i++ {
				conn.r.Reset(f.frame)
				if _, err := readFrame(conn); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkParseFrame(b *testing.B) {
	muteLogs(b)
	frames := []struct {
		name  string
		frame []byte
	}{
		{"method/basic.publish", formatMethodFrame(1, BASIC, TypeMethod(BASIC_PUBLISH), benchPublishPayload())},
		{"header", createHeaderFrame(1, uint16(BASIC), benchMessage(1024))},
		{"body/1KiB", createBodyFrame(1, make([]byte, 1<<10))},
	}
	for _, f := range frames {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := parseFrame(f.frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkParseBasicPublishFrame(b *testing.B) {
	muteLogs(b)
	payload := benchPublishPayload()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := parseBasicPublishFrame(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseBasicHeader(b *testing.B) {
	muteLogs(b)
	frame := createHeaderFrame(1, uint16(BASIC), benchMessage(1024))
	payload := frame[7 : len(frame)-1]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := parseBasicHeader(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCreateHeaderFrame(b *testing.B) {
	muteLogs(b)
	msg := benchMessage(1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = createHeaderFrame(1, uint16(BASIC), msg)
	}
}

func BenchmarkCreateBodyFrame(b *testing.B) {
	muteLogs(b)
	for _, n := range benchPayloadSizes {
		b.Run(sizeName(n), func(b *testing.B) {
			body := make([]byte, n)
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				_ = createBodyFrame(1, body)
			}
		})
	}
}

func BenchmarkCreateBasicDeliverFrame(b *testing.B) {
	muteLogs(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = createBasicDeliverFrame(1, "ctag-bench", "amq.direct", "bench.routing.key", uint64(i), false)
	}
}
