package amqp

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeShortStrByteBoundary(t *testing.T) {
	for _, tt := range []struct {
		name   string
		data   string
		reject bool
	}{
		{"empty", "", false},
		{"255 bytes", strings.Repeat("a", 255), false},
		{"256 bytes", strings.Repeat("a", 256), true},
		{"255 multibyte bytes", strings.Repeat("\u00e9", 127) + "a", false},
		{"256 multibyte bytes", strings.Repeat("\u00e9", 128), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			buf.WriteString("existing prefix")
			before := bytes.Clone(buf.Bytes())
			err := EncodeShortStr(&buf, tt.data)
			if (err != nil) != tt.reject {
				t.Fatalf("EncodeShortStr error = %v, want rejection %v", err, tt.reject)
			}
			if tt.reject {
				if !bytes.Equal(buf.Bytes(), before) {
					t.Fatalf("rejected shortstr changed buffer: length %d, want %d", buf.Len(), len(before))
				}
				return
			}
			want := append(before, byte(len(tt.data)))
			want = append(want, tt.data...)
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("encoded shortstr mismatch for %d-byte input", len(tt.data))
			}
		})
	}
}

func requireEncoded(t testing.TB) func([]byte, error) []byte {
	t.Helper()
	return func(data []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
}

func TestEncodeTableRejectsLongFieldNames(t *testing.T) {
	longKey := strings.Repeat("\u00e9", 128)
	for _, tt := range []struct {
		name  string
		table map[string]any
	}{
		{"field name", map[string]any{longKey: "value"}},
		{"nested table", map[string]any{"nested": map[string]any{longKey: true}}},
		{"array of tables", map[string]any{"nested": []map[string]any{{longKey: true}}}},
		{"mixed array", map[string]any{"nested": []any{"prefix", map[string]any{longKey: true}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := EncodeTable(tt.table)
			if err == nil || !strings.Contains(err.Error(), "255 bytes") {
				t.Fatalf("expected shortstr length error, got %v", err)
			}
			if data != nil {
				t.Errorf("failed encoding returned partial table: %d bytes", len(data))
			}
		})
	}
}

func TestEncodeBasicPropertiesRejectsLongShortStr(t *testing.T) {
	long := strings.Repeat("x", 256)
	for _, tt := range []struct {
		name  string
		props BasicProperties
	}{
		{"ContentType", BasicProperties{ContentType: ContentType(long)}},
		{"ContentEncoding", BasicProperties{ContentEncoding: long}},
		{"CorrelationID", BasicProperties{CorrelationID: long}},
		{"ReplyTo", BasicProperties{ReplyTo: long}},
		{"Expiration", BasicProperties{Expiration: long}},
		{"MessageID", BasicProperties{MessageID: long}},
		{"Type", BasicProperties{Type: long}},
		{"UserID", BasicProperties{UserID: long}},
		{"AppID", BasicProperties{AppID: long}},
		{"Reserved", BasicProperties{Reserved: long}},
		{"Headers", BasicProperties{Headers: map[string]any{"nested": map[string]any{long: true}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, flags, err := tt.props.encodeBasicProperties()
			if err == nil || !strings.Contains(err.Error(), "255 bytes") {
				t.Fatalf("expected shortstr length error, got %v", err)
			}
			if data != nil || flags != 0 {
				t.Errorf("failed encoding returned partial properties: %d bytes, flags %x", len(data), flags)
			}
			frame, err := createHeaderFrame(1, uint16(BASIC), Message{Properties: tt.props})
			if err == nil || frame != nil {
				t.Errorf("header frame = %v, error = %v; want nil frame and error", frame, err)
			}
		})
	}
}

func TestFormatMethodFrameRejectsLongShortStr(t *testing.T) {
	for _, kv := range []KeyValue{
		{Key: STRING_SHORT, Value: strings.Repeat("x", 256)},
		{Key: TABLE, Value: map[string]any{strings.Repeat("x", 256): true}},
	} {
		content := ContentList{KeyValuePairs: []KeyValue{{Key: INT_SHORT, Value: uint16(200)}, kv}}
		payload, err := formatMethodPayload(content)
		if err == nil || payload != nil {
			t.Fatalf("method payload = %v, error = %v; want nil payload and error", payload, err)
		}
		frame, err := (ResponseMethodMessage{Channel: 1, ClassID: uint16(CHANNEL), MethodID: uint16(CHANNEL_CLOSE), Content: content}).FormatMethodFrame()
		if err == nil || frame != nil {
			t.Errorf("method frame = %v, error = %v; want nil frame and error", frame, err)
		}
	}
}

func TestConnectionStartRejectsLongFieldName(t *testing.T) {
	frame, err := createConnectionStartFrame(HandshakeConfig{
		Mechanisms: []string{"PLAIN"}, Locales: []string{"en_US"},
		ServerProperties: map[string]any{strings.Repeat("x", 256): true},
	})
	if err == nil || frame != nil {
		t.Errorf("connection.start frame = %v, error = %v; want nil frame and error", frame, err)
	}
}
