package nettrace

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"
)

func readFixture(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/spin.nettrace")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t testing.TB) *Trace {
	t.Helper()
	trace, err := Parse(bytes.NewReader(readFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	return trace
}

// innermostIn returns the sample's innermost frame whose assembly name starts with prefix.
func innermostIn(sample Sample, prefix string) (Frame, bool) {
	for _, frame := range sample.Frames {
		if strings.HasPrefix(frame.Assembly, prefix) {
			return frame, true
		}
	}
	return Frame{}, false
}

func TestParseSpinTrace(t *testing.T) {
	t.Parallel()
	trace := parseFixture(t)
	if len(trace.Samples) == 0 {
		t.Fatal("trace has no samples")
	}
	threadCounts := map[uint64]int{}
	var managed, external int
	for _, sample := range trace.Samples {
		if sample.Managed {
			managed++
		} else {
			external++
		}
		if frame, ok := innermostIn(sample, "Spin"); ok && (strings.HasSuffix(frame.Method, "Alpha") || strings.HasSuffix(frame.Method, "Beta")) {
			threadCounts[sample.ThreadID]++
		}
	}
	if managed == 0 || external == 0 {
		t.Errorf("managed = %d, external = %d, want both kinds in the fixture", managed, external)
	}
	var mainThread uint64
	mainCount := 0
	for thread, count := range threadCounts {
		if count > mainCount {
			mainThread, mainCount = thread, count
		}
	}
	if mainCount == 0 {
		t.Fatal("could not identify the fixture main thread")
	}
	var alphaNanos, betaNanos uint64
	for index := 0; index+1 < len(trace.Samples); index++ {
		sample := trace.Samples[index]
		if sample.ThreadID != mainThread || trace.Samples[index+1].Timestamp <= sample.Timestamp {
			continue
		}
		frame, _ := innermostIn(sample, "Spin")
		switch {
		case strings.HasSuffix(frame.Method, "Alpha"):
			alphaNanos += trace.Samples[index+1].Timestamp - sample.Timestamp
		case strings.HasSuffix(frame.Method, "Beta"):
			betaNanos += trace.Samples[index+1].Timestamp - sample.Timestamp
		}
	}
	// The fixture spins Alpha for 600 ms and Beta for 300 ms, so the totals check the timestamp scale too.
	if alphaNanos < 400_000_000 || alphaNanos > 700_000_000 || betaNanos < 200_000_000 || betaNanos > 350_000_000 {
		t.Fatalf("sampled times are off scale: alpha=%dns beta=%dns", alphaNanos, betaNanos)
	}
}

func TestParseRejectsNonNettrace(t *testing.T) {
	t.Parallel()
	if _, err := Parse(strings.NewReader("not a nettrace")); !errors.Is(err, ErrNotNettrace) {
		t.Fatalf("err = %v, want ErrNotNettrace", err)
	}
}

func TestParseRefusesUnsupportedStreams(t *testing.T) {
	t.Parallel()
	minReader6 := readFixture(t)
	binary.LittleEndian.PutUint32(minReader6[traceFirstObject+7:], 6)
	notTrace := readFixture(t)
	copy(notTrace[traceFirstObject+15:], "Xrace")
	for name, tc := range map[string]struct {
		data []byte
		want error
	}{
		"too short":              {[]byte("Nettra"), ErrNotNettrace},
		"no FastSerialization":   {append([]byte("Nettrace"), bytes.Repeat([]byte{0}, 64)...), ErrUnsupportedVersion},
		"wrong serializer":       {append([]byte("Nettrace\x00\x00\x00\x00!FastSerialization.9"), bytes.Repeat([]byte{0}, 64)...), ErrUnsupportedVersion},
		"reader version 6":       {minReader6, ErrUnsupportedVersion},
		"first object not Trace": {notTrace, ErrUnsupportedVersion},
		"no blocks":              {append([]byte("Nettrace\x14\x00\x00\x00!FastSerialization.1"), bytes.Repeat([]byte{0}, 64)...), ErrUnsupportedVersion},
	} {
		if _, err := Parse(bytes.NewReader(tc.data)); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestParseRefusesAStreamWithoutBlocks(t *testing.T) {
	t.Parallel()
	data := readFixture(t)
	// Cut inside the Trace object, so no block follows it.
	if _, err := Parse(bytes.NewReader(data[:traceFirstObject+60])); !errors.Is(err, ErrNoEvents) {
		t.Fatalf("err = %v, want ErrNoEvents", err)
	}
}

func TestParseCompressedEvent(t *testing.T) {
	t.Parallel()
	state := v5EventState{}
	body := []byte{0x88, 3, 10, 2, 0xaa, 0xbb}
	event, used, ok := parseCompressedV5Event(body, &state)
	if !ok || used != len(body) {
		t.Fatalf("event parse = %v, used %d", ok, used)
	}
	if event.stackID != 3 || event.timestamp != 10 || string(event.payload) != "\xaa\xbb" {
		t.Fatalf("event = %+v", event)
	}
}

func TestParseMetadataRecord(t *testing.T) {
	t.Parallel()
	payload := binary.LittleEndian.AppendUint32(nil, 7)
	payload = appendWideForTest(payload, "Provider")
	payload = binary.LittleEndian.AppendUint32(payload, 143)
	payload = appendWideForTest(payload, "MethodLoadVerbose")
	metadata := map[uint32]eventMetadata{}
	parseV5Metadata(payload, metadata)
	if got := metadata[7]; got.provider != "Provider" || got.eventID != 143 {
		t.Fatalf("metadata = %+v", got)
	}
	cut := map[uint32]eventMetadata{}
	parseV5Metadata(payload[:len(payload)-2], cut)
	if len(cut) != 0 {
		t.Fatalf("a record cut inside its event name was kept: %+v", cut)
	}
}

func TestParseStackRecord(t *testing.T) {
	t.Parallel()
	block := binary.LittleEndian.AppendUint32(nil, 4)
	block = binary.LittleEndian.AppendUint32(block, 1)
	block = binary.LittleEndian.AppendUint32(block, 16)
	block = binary.LittleEndian.AppendUint64(block, 0x1000)
	block = binary.LittleEndian.AppendUint64(block, 0x2000)
	stacks := parseV5StackBlock(block, 8)
	if len(stacks) != 1 || len(stacks[0].frames) != 2 || stacks[0].frames[1] != 0x2000 {
		t.Fatalf("stacks = %+v", stacks)
	}
	// A 4-byte-pointer trace stores four bytes per frame.
	narrow := binary.LittleEndian.AppendUint32(nil, 0)
	narrow = binary.LittleEndian.AppendUint32(narrow, 1)
	narrow = binary.LittleEndian.AppendUint32(narrow, 8)
	narrow = binary.LittleEndian.AppendUint32(narrow, 0x1000)
	narrow = binary.LittleEndian.AppendUint32(narrow, 0x2000)
	stacks = parseV5StackBlock(narrow, 4)
	if len(stacks) != 1 || len(stacks[0].frames) != 2 || stacks[0].frames[1] != 0x2000 {
		t.Fatalf("narrow stacks = %+v", stacks)
	}
}

func appendWideForTest(data []byte, value string) []byte {
	for _, r := range value {
		data = append(data, byte(r&0xff), byte((r>>8)&0xff))
	}
	return append(data, 0, 0)
}

// uncompressedBody is an uncompressed event header after its EventSize, with a payload of the given size.
func uncompressedBody(metadataID uint32, thread uint64, payload int) []byte {
	body := make([]byte, 4+4+8+8+4+4+8+16+16+4+payload)
	binary.LittleEndian.PutUint32(body, metadataID)
	binary.LittleEndian.PutUint64(body[8:], thread)
	binary.LittleEndian.PutUint32(body[4+4+8+8+4+4+8+16+16:], smallU32(payload))
	return body
}

func smallU32(n int) uint32 { return uint32FromUint64(uint64FromInt(n)) }

func appendEvent(data, body []byte, pad int) []byte {
	data = binary.LittleEndian.AppendUint32(data, smallU32(len(body)))
	data = append(data, body...)
	return append(data, make([]byte, pad)...)
}

func TestUnsortedEventKeepsItsMetadataID(t *testing.T) {
	t.Parallel()
	// The runtime sets the high bit of MetadataId on an unsorted event and counts its padding inside EventSize.
	data := appendEvent(nil, uncompressedBody(7|1<<31, 1, 4), 0)
	events := parseV5Events(data, nil, false, false)
	if len(events) != 1 || events[0].metadata != 7 {
		t.Fatalf("events = %+v, want one with metadata 7", events)
	}
}

func TestEventsPaddedOutsideEventSizeAreAllRead(t *testing.T) {
	t.Parallel()
	// The spec text reads as padding outside EventSize: 4+EventSize is then unaligned and the next event follows
	// the padding.
	data := appendEvent(nil, uncompressedBody(7, 1, 3), 1)
	data = appendEvent(data, uncompressedBody(8, 2, 4), 0)
	events := parseV5Events(data, nil, false, false)
	if len(events) != 2 || events[0].metadata != 7 || events[1].metadata != 8 || events[1].threadID != 2 {
		t.Fatalf("events = %+v, want metadata 7 then 8", events)
	}
}

func TestThreadIDKeepsAll64Bits(t *testing.T) {
	t.Parallel()
	const thread = 0x1_0000_0001
	data := appendEvent(nil, uncompressedBody(7, thread, 0), 0)
	events := parseV5Events(data, nil, false, false)
	if len(events) != 1 || events[0].threadID != thread {
		t.Fatalf("events = %+v, want thread %#x", events, uint64(thread))
	}
}

func TestStackBlockCountIsCheckedBeforeAllocating(t *testing.T) {
	block := binary.LittleEndian.AppendUint32(nil, 0)
	block = binary.LittleEndian.AppendUint32(block, 1_000_000)
	if allocs := testing.AllocsPerRun(1, func() { _ = parseV5StackBlock(block, 8) }); allocs != 0 {
		t.Fatalf("a lying stack count allocated %v times", allocs)
	}
}

func TestSpinLoaderEventsAreNotConflated(t *testing.T) {
	t.Parallel()
	trace := parseFixture(t)
	if len(trace.Assemblies) == 0 || len(trace.Modules) == 0 {
		t.Fatalf("assemblies = %d, modules = %d", len(trace.Assemblies), len(trace.Modules))
	}
	for id, name := range trace.Assemblies {
		if strings.ContainsAny(name, `/\`) || strings.HasSuffix(strings.ToLower(name), ".dll") {
			t.Errorf("assembly %#x is a module path: %q", id, name)
		}
	}
	targets := map[uint64]bool{}
	for module, assembly := range trace.Modules {
		if _, ok := trace.Assemblies[assembly]; !ok {
			t.Errorf("module %#x maps to %#x, which is no assembly", module, assembly)
		}
		targets[assembly] = true
	}
	for id := range trace.Assemblies {
		if !targets[id] {
			t.Errorf("assembly %#x is the target of no module", id)
		}
	}
}

func TestRuntimeAndRundownNumberTheirEventsDifferently(t *testing.T) {
	t.Parallel()
	module := binary.LittleEndian.AppendUint64(nil, 0x10)
	module = binary.LittleEndian.AppendUint64(module, 0x20)
	trace := &Trace{Assemblies: map[uint64]string{}, Modules: map[uint64]uint64{}}
	// 154 is AssemblyLoad on the runtime provider but ModuleDCEnd on the rundown provider.
	decodeRuntimeEvent(trace, ProviderRuntime, 154, module)
	if len(trace.Modules) != 0 {
		t.Errorf("runtime event 154 decoded as a module: %v", trace.Modules)
	}
	decodeRuntimeEvent(trace, providerRundown, 154, module)
	if trace.Modules[0x10] != 0x20 {
		t.Errorf("rundown event 154 not decoded as a module: %v", trace.Modules)
	}
}
