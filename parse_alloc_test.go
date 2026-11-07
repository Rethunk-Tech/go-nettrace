package nettrace

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"unicode/utf16"
)

func v5Object(out []byte, name string, block []byte) []byte {
	out = append(out, 5, 5, 1)
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, sizeU32(len(name)))
	out = append(out, name...)
	out = append(out, 6)
	out = binary.LittleEndian.AppendUint32(out, sizeU32(len(block)))
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return append(out, block...)
}

func wideNull(data []byte, s string) []byte {
	for _, unit := range utf16.Encode([]rune(s)) {
		data = binary.LittleEndian.AppendUint16(data, unit)
	}
	return append(data, 0, 0)
}

// sizeU32 narrows a test size, which is always small.
func sizeU32(n int) uint32 {
	if n < 0 || int64(n) > math.MaxUint32 {
		panic("size out of range")
	}
	return uint32(n)
}

func eventBlockHeader(compressed bool) []byte {
	h := binary.LittleEndian.AppendUint16(nil, 20)
	flags := uint16(0)
	if compressed {
		flags = 1
	}
	h = binary.LittleEndian.AppendUint16(h, flags)
	return append(h, make([]byte, 16)...)
}

// minimalSampleStream is the smallest stream per sample the format allows: after the first event, a compressed
// sample event is 2 bytes (flags 0, timestamp delta 1) that reuses the previous event's metadata and stack.
func minimalSampleStream(t testing.TB, samples int) []byte {
	t.Helper()
	payload := binary.LittleEndian.AppendUint32(nil, 1)
	payload = wideNull(payload, ProviderSampleProfiler)
	payload = binary.LittleEndian.AppendUint32(payload, 0)
	payload = wideNull(payload, "ThreadSample")
	for len(payload)%4 != 0 {
		payload = append(payload, 0)
	}
	meta := eventBlockHeader(false)
	meta = binary.LittleEndian.AppendUint32(meta, sizeU32(76+len(payload)))
	body := make([]byte, 76)
	binary.LittleEndian.PutUint32(body[72:], sizeU32(len(payload)))
	meta = append(append(meta, body...), payload...)

	data := v5Object(bytes.Clone(readFixture(t)[:0x66]), "MetadataBlock", meta)
	stack := binary.LittleEndian.AppendUint32(nil, 1)
	stack = binary.LittleEndian.AppendUint32(stack, 1)
	stack = binary.LittleEndian.AppendUint32(stack, 8)
	stack = binary.LittleEndian.AppendUint64(stack, 0x1000)
	data = v5Object(data, "StackBlock", stack)

	events := eventBlockHeader(true)
	events = append(events, 0x8f, 1, 0, 0, 0, 1, 1, 0, 0)
	for range samples - 1 {
		events = append(events, 0, 1)
	}
	return v5Object(data, "EventBlock", events)
}

const amplificationSamples = 1_000_000

func TestMinimalSampleStreamParses(t *testing.T) {
	trace, err := Parse(bytes.NewReader(minimalSampleStream(t, 1000)))
	if err != nil || len(trace.Samples) != 1000 {
		t.Fatalf("samples = %d, err = %v", len(trace.Samples), err)
	}
}

// BenchmarkParseMinimalSamples measures the worst case of SECURITY.md's memory statement: allocated bytes per input
// byte for a stream of nothing but 2-byte sample events.
func BenchmarkParseMinimalSamples(b *testing.B) {
	data := minimalSampleStream(b, amplificationSamples)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
