package amqp

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"runtime"
	"testing"
)

func TestDecodeFlags_QueueOrder(t *testing.T) {
	// passive, durable, exclusive, autoDelete, noWait
	flagNames := []string{"passive", "durable", "exclusive", "autoDelete", "noWait"}
	// 0b00011110: durable, exclusive, autoDelete, noWait set
	octet := byte(0x1E)
	flags := DecodeFlags(octet, flagNames, true)
	if flags["passive"] {
		t.Errorf("Expected passive=false, got true")
	}
	if !flags["durable"] {
		t.Errorf("Expected durable=true, got false")
	}
	if !flags["exclusive"] {
		t.Errorf("Expected exclusive=true, got false")
	}
	if !flags["autoDelete"] {
		t.Errorf("Expected autoDelete=true, got false")
	}
	if !flags["noWait"] {
		t.Errorf("Expected noWait=true, got false")
	}
}

func TestDecodeFlags_ExchangeOrder(t *testing.T) {
	// passive, durable, autoDelete, internal, noWait
	flagNames := []string{"passive", "durable", "autoDelete", "internal", "noWait"}
	// 0b00011110: durable, autoDelete, internal, noWait set
	octet := byte(0x1E)
	flags := DecodeFlags(octet, flagNames, true)
	if flags["passive"] {
		t.Errorf("Expected passive=false, got true")
	}
	if !flags["durable"] {
		t.Errorf("Expected durable=true, got false")
	}
	if !flags["autoDelete"] {
		t.Errorf("Expected autoDelete=true, got false")
	}
	if !flags["internal"] {
		t.Errorf("Expected internal=true, got false")
	}
	if !flags["noWait"] {
		t.Errorf("Expected noWait=true, got false")
	}
}

func TestDecodeFlags_AllFalse(t *testing.T) {
	flagNames := []string{"passive", "durable", "exclusive", "autoDelete", "noWait"}
	octet := byte(0x00)
	flags := DecodeFlags(octet, flagNames, true)
	for _, name := range flagNames {
		if flags[name] {
			t.Errorf("Expected %s=false, got true", name)
		}
	}
}

func TestDecodeFlags_AllTrue(t *testing.T) {
	flagNames := []string{"passive", "durable", "exclusive", "autoDelete", "noWait"}
	octet := byte(0x1F)
	flags := DecodeFlags(octet, flagNames, true)
	for _, name := range flagNames {
		if !flags[name] {
			t.Errorf("Expected %s=true, got false", name)
		}
	}
}

func TestParseQueueDeclareFrame_Flags(t *testing.T) {
	// passive, durable, exclusive, autoDelete, noWait
	var payload bytes.Buffer
	payload.Write([]byte{0, 0}) // reserved
	payload.WriteByte(4)        // queue name length
	payload.WriteString("test")
	payload.WriteByte(0x1E)           // flags: durable, exclusive, autoDelete, noWait
	payload.Write([]byte{0, 0, 0, 0}) // arguments (empty longstr)

	request, err := parseQueueDeclareFrame(payload.Bytes())
	if err != nil {
		t.Fatalf("parseQueueDeclareFrame failed: %v", err)
	}
	msg, ok := request.Content.(*QueueDeclareMessage)
	if !ok {
		t.Fatalf("Expected *QueueDeclareMessage, got %T", request.Content)
	}
	if msg.Passive {
		t.Errorf("Expected passive=false, got true")
	}
	if !msg.Durable {
		t.Errorf("Expected durable=true, got false")
	}
	if !msg.Exclusive {
		t.Errorf("Expected exclusive=true, got false")
	}
	if !msg.AutoDelete {
		t.Errorf("Expected autoDelete=true, got false")
	}
	if !msg.NoWait {
		t.Errorf("Expected noWait=true, got false")
	}
}

func TestParseExchangeDeclareFrame_Flags(t *testing.T) {
	// passive, durable, autoDelete, internal, noWait
	var payload bytes.Buffer
	payload.Write([]byte{0, 0}) // reserved
	payload.WriteByte(7)        // exchange name length
	payload.WriteString("exchng1")
	payload.WriteByte(6) // type length
	payload.WriteString("direct")
	payload.WriteByte(0x1E)           // flags: durable, autoDelete, internal, noWait
	payload.Write([]byte{0, 0, 0, 0}) // arguments (empty longstr)

	request, err := parseExchangeDeclareFrame(payload.Bytes())
	if err != nil {
		t.Fatalf("parseExchangeDeclareFrame failed: %v", err)
	}
	msg, ok := request.Content.(*ExchangeDeclareMessage)
	if !ok {
		t.Fatalf("Expected *ExchangeDeclareMessage, got %T", request.Content)
	}
	if msg.Passive {
		t.Errorf("Expected passive=false, got true")
	}
	if !msg.Durable {
		t.Errorf("Expected durable=true, got false")
	}
	if !msg.AutoDelete {
		t.Errorf("Expected autoDelete=true, got false")
	}
	if !msg.Internal {
		t.Errorf("Expected internal=true, got false")
	}
	if !msg.NoWait {
		t.Errorf("Expected noWait=true, got false")
	}
}

// Truncated variable-length fields: the declared length is read from the wire
// and exceeds the bytes that follow. A short read from a bytes.Reader returns
// no error, so these inputs used to be accepted with NUL padding.
func TestDecodeTable_RejectsTruncatedFields(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"short string body", []byte{1, 'a', 's', 10, 'x'}},
		{"long string body", []byte{1, 'a', 'S', 0, 0, 0, 9, 'x', 'y'}},
		{"long long int body", []byte{1, 'a', 'L', 0, 0, 0, 1}},
		{"field name", []byte{5, 'a'}},
		// Nested table that declares 6 bytes and carries 5: the short read is
		// padded with NUL, and the padding is absorbed by the inner long-ish
		// string, so nothing downstream reports a problem.
		{"nested table", []byte{1, 'a', 'F', 0, 0, 0, 6, 1, 'b', 's', 3, 'y'}},
		// Same with an array body: declared 6, carried 3, padding absorbed by
		// the inner short string.
		{"array", []byte{1, 'a', 'A', 0, 0, 0, 6, 's', 5, 'x'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table, err := DecodeTable(tt.data)
			if err == nil {
				t.Fatalf("DecodeTable(%v) = %q, want an error for truncated input", tt.data, table)
			}
		})
	}
}

func TestDecodeArray_RejectsTruncatedElements(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"short string", []byte{'s', 10, 'x'}},
		{"long string", []byte{'S', 0, 0, 0, 9, 'x', 'y'}},
		// The NUL padding of a short table read is absorbed by the inner string.
		{"nested table", []byte{'F', 0, 0, 0, 6, 1, 'b', 's', 3, 'y'}},
		{"nested array", []byte{'A', 0, 0, 0, 6, 's', 5, 'x'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			array, err := DecodeArray(tt.data)
			if err == nil {
				t.Fatalf("DecodeArray(%v) = %q, want an error for truncated input", tt.data, array)
			}
		})
	}
}

func TestDecodeShortStr_RejectsTruncatedInput(t *testing.T) {
	if got, err := DecodeShortStr(bytes.NewReader([]byte{10, 'x'})); err == nil {
		t.Fatalf("DecodeShortStr = %q, want an error for a truncated string", got)
	}
}

func TestDecodeLongStr_RejectsTruncatedInput(t *testing.T) {
	if got, err := DecodeLongStr(bytes.NewReader([]byte{0, 0, 0, 9, 'x', 'y'})); err == nil {
		t.Fatalf("DecodeLongStr = %q, want an error for a truncated string", got)
	}
}

// A declared length larger than the rest of the frame is rejected before the
// field body is allocated. The large cases declare 64 MiB, far more than the
// few bytes that follow, so allocating it would show up in TotalAlloc. Short
// strings can declare at most 255 bytes, less than the decoder's own overhead,
// so those cases compare a declared 255 with a declared 4 (also more than is
// present) over many calls: the difference must stay below half of 255.
func TestDecode_OversizedLengthIsNotAllocated(t *testing.T) {
	const large = 64 << 20
	be32 := func(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	body := []byte("xyz")

	table := func(data []byte) error { _, err := DecodeTable(data); return err }
	array := func(data []byte) error { _, err := DecodeArray(data); return err }
	longStr := func(data []byte) error { _, err := DecodeLongStr(bytes.NewReader(data)); return err }
	shortStr := func(data []byte) error { _, err := DecodeShortStr(bytes.NewReader(data)); return err }

	tests := []struct {
		name     string
		declared uint32
		input    func(n uint32) []byte
		decode   func([]byte) error
	}{
		{"table array field", large, func(n uint32) []byte { return cat([]byte{1, 'a', 'A'}, be32(n), body) }, table},
		{"table long string field", large, func(n uint32) []byte { return cat([]byte{1, 'a', 'S'}, be32(n), body) }, table},
		{"table nested table field", large, func(n uint32) []byte { return cat([]byte{1, 'a', 'F'}, be32(n), body) }, table},
		{"array nested table", large, func(n uint32) []byte { return cat([]byte{'F'}, be32(n), body) }, array},
		{"array nested array", large, func(n uint32) []byte { return cat([]byte{'A'}, be32(n), body) }, array},
		{"long string", large, func(n uint32) []byte { return cat(be32(n), body) }, longStr},
		{"table field name", 255, func(n uint32) []byte { return cat([]byte{byte(n)}, body) }, table},
		{"table short string field", 255, func(n uint32) []byte { return cat([]byte{1, 'a', 's', byte(n)}, body) }, table},
		{"short string", 255, func(n uint32) []byte { return cat([]byte{byte(n)}, body) }, shortStr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.input(tt.declared)
			var err error
			measure := func(calls int, data []byte) uint64 {
				return allocatedPerCall(calls, func() { err = tt.decode(data) })
			}

			var extra, limit uint64
			if tt.declared >= 1<<20 {
				extra, limit = measure(1, data), 1<<20
			} else {
				baseline := measure(1000, tt.input(uint32(len(body)+1)))
				if extra = measure(1000, data); extra > baseline {
					extra -= baseline
				} else {
					extra = 0
				}
				limit = uint64(tt.declared / 2)
			}

			if err == nil {
				t.Fatalf("decode succeeded, want an error for a length of %d with %d bytes present", tt.declared, len(body))
			}
			if extra >= limit {
				t.Errorf("decode allocated %d bytes per call for a declared length of %d, want under %d", extra, tt.declared, limit)
			}
		})
	}
}

// allocatedPerCall returns the bytes allocated per call of f, averaged over n calls.
func allocatedPerCall(n int, f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < n; i++ {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(n)
}

// Nested tables and arrays are decoded in place, so decoding deep but valid
// nesting allocates in proportion to the input, not to the input times the
// depth. 3000 levels is about 15 KB of input; copying the rest of the body at
// every level would allocate over 20 MB.
func TestDecode_NestedValuesAllocateLinearly(t *testing.T) {
	const levels = 3000
	table := func(data []byte) error { _, err := DecodeTable(data); return err }
	array := func(data []byte) error { _, err := DecodeArray(data); return err }

	tests := []struct {
		name   string
		data   []byte
		decode func([]byte) error
	}{
		{"arrays in arrays", nestedArrays(levels), array},
		{"tables in tables", nestedTables(levels), table},
		{"tables and arrays alternating", nestedMixed(levels, nil), table},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			allocated := allocatedPerCall(1, func() { err = tt.decode(tt.data) })
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if limit := uint64(128 * len(tt.data)); allocated >= limit {
				t.Errorf("decode allocated %d bytes for %d bytes of input, want under %d", allocated, len(tt.data), limit)
			}
		})
	}
}

// An error deep inside nested values is returned as it is, not wrapped once per
// level: wrapping copies the growing message at every level, so both the
// message and the memory spent building it would grow with the square of the
// depth.
func TestDecode_NestedErrorIsNotWrappedPerLevel(t *testing.T) {
	data := nestedMixed(3000, []byte{'Z'}) // invalid element in the innermost array

	var err error
	allocated := allocatedPerCall(1, func() { _, err = DecodeTable(data) })
	if err == nil {
		t.Fatal("DecodeTable succeeded, want an error for the invalid innermost element")
	}
	if len(err.Error()) > 256 {
		t.Errorf("error message is %d bytes long, want a short message: %.80s...", len(err.Error()), err.Error())
	}
	if limit := uint64(128 * len(data)); allocated >= limit {
		t.Errorf("DecodeTable allocated %d bytes for %d bytes of input, want under %d", allocated, len(data), limit)
	}
}

// Control cases: fields whose declared length matches the bytes present still
// decode, so the truncation check cannot be satisfied by rejecting everything.
func TestDecodeTable_AcceptsCompleteFields(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want map[string]interface{}
	}{
		{"short string", []byte{1, 'a', 's', 1, 'x'}, map[string]interface{}{"a": "x"}},
		{"long string", []byte{1, 'a', 'S', 0, 0, 0, 2, 'x', 'y'}, map[string]interface{}{"a": "xy"}},
		{"empty short string", []byte{1, 'a', 's', 0}, map[string]interface{}{"a": ""}},
		{"multi byte field name", []byte{3, 'f', 'o', 'o', 's', 1, 'x'}, map[string]interface{}{"foo": "x"}},
		{"nested table", []byte{1, 'a', 'F', 0, 0, 0, 5, 1, 'b', 's', 1, 'y'}, map[string]interface{}{"a": map[string]interface{}{"b": "y"}}},
		{"array", []byte{1, 'a', 'A', 0, 0, 0, 3, 's', 1, 'z'}, map[string]interface{}{"a": []interface{}{"z"}}},
		{"long long int", []byte{1, 'a', 'L', 0, 0, 0, 0, 0, 0, 0, 42}, map[string]interface{}{"a": int64(42)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeTable(tt.data)
			if err != nil {
				t.Fatalf("DecodeTable(%v) failed: %v", tt.data, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecodeTable(%v) = %#v, want %#v", tt.data, got, tt.want)
			}
		})
	}
}

func TestDecodeArray_AcceptsCompleteElements(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []interface{}
	}{
		{"short string", []byte{'s', 1, 'x'}, []interface{}{"x"}},
		{"long string", []byte{'S', 0, 0, 0, 2, 'x', 'y'}, []interface{}{"xy"}},
		{"nested table", []byte{'F', 0, 0, 0, 5, 1, 'b', 's', 1, 'y'}, []interface{}{map[string]interface{}{"b": "y"}}},
		{"nested array", []byte{'A', 0, 0, 0, 3, 's', 1, 'z'}, []interface{}{[]interface{}{"z"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeArray(tt.data)
			if err != nil {
				t.Fatalf("DecodeArray(%v) failed: %v", tt.data, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecodeArray(%v) = %#v, want %#v", tt.data, got, tt.want)
			}
		})
	}
}

func TestDecodeStr_AcceptsCompleteInput(t *testing.T) {
	got, err := DecodeShortStr(bytes.NewReader([]byte{2, 'h', 'i'}))
	if err != nil {
		t.Fatalf("DecodeShortStr failed: %v", err)
	}
	if got != "hi" {
		t.Errorf("DecodeShortStr = %q, want %q", got, "hi")
	}

	got, err = DecodeLongStr(bytes.NewReader([]byte{0, 0, 0, 2, 'h', 'i'}))
	if err != nil {
		t.Fatalf("DecodeLongStr failed: %v", err)
	}
	if got != "hi" {
		t.Errorf("DecodeLongStr = %q, want %q", got, "hi")
	}

	got, err = DecodeLongStr(bytes.NewReader([]byte{0, 0, 0, 0}))
	if err != nil {
		t.Fatalf("DecodeLongStr with zero length failed: %v", err)
	}
	if got != "" {
		t.Errorf("DecodeLongStr with zero length = %q, want an empty string", got)
	}
}

// FuzzDecodeTable decodes arbitrary input as a field table and as an array.
// Neither may panic, and neither may allocate much more than the input could
// hold: a declared length is never allocated before its bytes are present.
// Every nested table or array copies the rest of its body, at most the whole
// input per level, and a level takes at least 5 bytes, so valid nesting may
// allocate up to len²/5 on top of the per-byte overhead.
func FuzzDecodeTable(f *testing.F) {
	seeds := [][]byte{
		{1, 'a', 's', 10, 'x'},
		{1, 'a', 'S', 0, 0, 0, 9, 'x', 'y'},
		{1, 'a', 'L', 0, 0, 0, 1},
		{5, 'a'},
		{1, 'a', 'F', 0, 0, 0, 6, 1, 'b', 's', 3, 'y'},
		{1, 'a', 'A', 0, 0, 0, 6, 's', 5, 'x'},
		{1, 'a', 'F', 0, 0, 0, 5, 1, 'b', 's', 1, 'y'},
		{1, 'a', 'A', 0, 0, 0, 3, 's', 1, 'z'},
		{'A', 0, 0, 0, 3, 's', 1, 'z'},
		{1, 'a', 'A', 4, 0, 0, 0, 'x', 'y', 'z'}, // declares 64 MiB
		nestedArrays(3000),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		n := uint64(len(data))
		limit := 1<<20 + 512*n + n*n/5
		if got := allocatedPerCall(1, func() { _, _ = DecodeTable(data) }); got > limit {
			t.Errorf("DecodeTable allocated %d bytes for %d bytes of input", got, n)
		}
		if got := allocatedPerCall(1, func() { _, _ = DecodeArray(data) }); got > limit {
			t.Errorf("DecodeArray allocated %d bytes for %d bytes of input", got, n)
		}
	})
}

// nestedArrays returns levels arrays nested inside each other, each with a
// correct length, around an empty innermost array.
func nestedArrays(levels int) []byte {
	data := []byte{'A', 0, 0, 0, 0}
	for i := 1; i < levels; i++ {
		data = append(append([]byte{'A'}, binary.BigEndian.AppendUint32(nil, uint32(len(data)))...), data...)
	}
	return data
}

// nestedTables returns levels field tables nested inside each other, each
// holding one field "a" with the next table, around an empty innermost table.
func nestedTables(levels int) []byte {
	var data []byte
	for i := 1; i < levels; i++ {
		data = append(append([]byte{1, 'a', 'F'}, binary.BigEndian.AppendUint32(nil, uint32(len(data)))...), data...)
	}
	return data
}

// nestedMixed returns levels values nested inside each other, alternating
// between a table holding an array and an array holding a table, around the
// innermost array body inner. The outermost value is a table.
func nestedMixed(levels int, inner []byte) []byte {
	data := inner
	for i := 1; i < levels; i++ {
		length := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		if i%2 == 1 {
			data = append(append([]byte{1, 'a', 'A'}, length...), data...) // table holding an array
		} else {
			data = append(append([]byte{'F'}, length...), data...) // array holding a table
		}
	}
	return data
}
