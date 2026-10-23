package amqp

import (
	"bytes"
	"testing"
)

// The server-sent basic.cancel matches the spec: class 60, method 30,
// consumer-tag shortstr, then the nowait bit set.
func TestCreateBasicCancelFrame(t *testing.T) {
	frame := createBasicCancelFrame(3, "ctag-1")

	want := []byte{
		1,    // method frame
		0, 3, // channel
		0, 0, 0, 12, // payload size
		0, 60, // class basic
		0, 30, // method cancel
		6, 'c', 't', 'a', 'g', '-', '1', // consumer-tag
		1,    // nowait
		0xCE, // frame-end
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("basic.cancel frame\n got % x\nwant % x", frame, want)
	}

	// The broker's own decoder reads it back.
	got, err := parseBasicCancelFrame(frame[11 : len(frame)-1])
	if err != nil {
		t.Fatalf("parseBasicCancelFrame: %v", err)
	}
	content := got.Content.(*BasicCancelContent)
	if content.ConsumerTag != "ctag-1" || !content.NoWait {
		t.Errorf("decoded %+v, want consumer tag ctag-1 with nowait", content)
	}
}

func TestClientCapability(t *testing.T) {
	decode := func(t *testing.T, props map[string]any) map[string]any {
		t.Helper()
		decoded, err := DecodeTable(EncodeTable(props))
		if err != nil {
			t.Fatalf("DecodeTable: %v", err)
		}
		return decoded
	}
	tests := []struct {
		name  string
		props map[string]any
		want  bool
	}{
		{"advertised", map[string]any{"capabilities": map[string]any{"consumer_cancel_notify": true}}, true},
		{"set to false", map[string]any{"capabilities": map[string]any{"consumer_cancel_notify": false}}, false},
		{"other capabilities only", map[string]any{"capabilities": map[string]any{"publisher_confirms": true}}, false},
		{"no capabilities table", map[string]any{"product": "client"}, false},
		{"capabilities not a table", map[string]any{"capabilities": "consumer_cancel_notify"}, false},
		{"no properties", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := tt.props
			if props != nil {
				props = decode(t, props)
			}
			if got := clientCapability(props, "consumer_cancel_notify"); got != tt.want {
				t.Errorf("clientCapability = %v, want %v", got, tt.want)
			}
		})
	}
}
