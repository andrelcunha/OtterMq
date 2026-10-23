package amqp

import "testing"

// basic.recover and basic.recover-async carry one octet (the requeue bit).
// The parser used to require 9 bytes, as basic.ack does, so every
// basic.recover from a client failed to parse and dropped the connection.
func TestParseBasicRecoverFrame(t *testing.T) {
	for _, tc := range []struct {
		name    string
		async   bool
		payload []byte
		requeue bool
	}{
		{"recover requeue", false, []byte{1}, true},
		{"recover no requeue", false, []byte{0}, false},
		{"recover-async requeue", true, []byte{1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := parseBasicRecoverFrame
			if tc.async {
				parse = parseBasicRecoverAsyncFrame
			}
			req, err := parse(tc.payload)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			content, ok := req.Content.(*BasicRecoverContent)
			if !ok {
				t.Fatalf("content is %T", req.Content)
			}
			if content.Requeue != tc.requeue || content.Async != tc.async {
				t.Errorf("got requeue=%v async=%v, want %v %v", content.Requeue, content.Async, tc.requeue, tc.async)
			}
		})
	}
	if _, err := parseBasicRecoverFrame(nil); err == nil {
		t.Error("an empty payload parsed")
	}
}
