package amqp

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCloseFramesTruncateReplyTextAtRuneBoundary(t *testing.T) {
	framer := &DefaultFramer{}
	for _, builder := range []struct {
		name    string
		channel uint16
		class   uint16
		method  uint16
		build   func(uint16, uint16, uint16, uint16, string) ([]byte, error)
	}{
		{"channel", 1, uint16(CHANNEL), uint16(CHANNEL_CLOSE), framer.CreateChannelCloseFrame},
		{"connection", 0, uint16(CONNECTION), uint16(CONNECTION_CLOSE), framer.CreateConnectionCloseFrame},
	} {
		for _, tt := range []struct {
			name string
			text string
			want string
		}{
			{"empty", "", ""},
			{"short", "NOT_FOUND", "NOT_FOUND"},
			{"255 ASCII bytes", strings.Repeat("a", 255), strings.Repeat("a", 255)},
			{"256 ASCII bytes", strings.Repeat("a", 256), strings.Repeat("a", 255)},
			{"255 UTF8 bytes", strings.Repeat("\u00e9", 127) + "a", strings.Repeat("\u00e9", 127) + "a"},
			{"split two-byte rune", strings.Repeat("\u00e9", 128), strings.Repeat("\u00e9", 127)},
			{"split three-byte rune", strings.Repeat("a", 254) + "\u754c", strings.Repeat("a", 254)},
			{"split four-byte rune", strings.Repeat("a", 252) + "\U0001f9a6", strings.Repeat("a", 252)},
			{"four-byte rune ends at limit", strings.Repeat("a", 251) + "\U0001f9a6" + "tail", strings.Repeat("a", 251) + "\U0001f9a6"},
			{"three-byte rune ends at limit", strings.Repeat("\u754c", 86), strings.Repeat("\u754c", 85)},
		} {
			t.Run(builder.name+"/"+tt.name, func(t *testing.T) {
				frame, err := builder.build(builder.channel, uint16(NOT_FOUND), uint16(QUEUE), uint16(QUEUE_DECLARE), tt.text)
				if err != nil {
					t.Fatalf("create close frame: %v", err)
				}
				if len(frame) != 19+len(tt.want) || frame[0] != byte(TYPE_METHOD) || frame[len(frame)-1] != FRAME_END {
					t.Fatalf("invalid close frame framing: %x", frame)
				}
				if binary.BigEndian.Uint16(frame[1:3]) != builder.channel || int(binary.BigEndian.Uint32(frame[3:7])) != len(frame)-8 {
					t.Fatal("close frame channel or payload size changed")
				}
				payload := frame[7 : len(frame)-1]
				if binary.BigEndian.Uint16(payload[0:2]) != builder.class || binary.BigEndian.Uint16(payload[2:4]) != builder.method || binary.BigEndian.Uint16(payload[4:6]) != uint16(NOT_FOUND) {
					t.Fatal("close method or reply code changed")
				}
				length := int(payload[6])
				if length != len(tt.want) || string(payload[7:7+length]) != tt.want || !utf8.Valid(payload[7:7+length]) {
					t.Fatalf("reply text = %q (%d bytes), want %q", payload[7:7+length], length, tt.want)
				}
				if binary.BigEndian.Uint16(payload[7+length:9+length]) != uint16(QUEUE) || binary.BigEndian.Uint16(payload[9+length:11+length]) != uint16(QUEUE_DECLARE) {
					t.Fatal("truncation changed the failing class/method fields")
				}
			})
		}
	}
}
