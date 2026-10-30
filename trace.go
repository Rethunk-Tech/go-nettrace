package nettrace

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"io"
	"slices"
)

// The providers whose events the reader understands, and the keywords that make the runtime emit the loader and
// JIT events frame resolution needs.
const (
	ProviderSampleProfiler = "Microsoft-DotNETCore-SampleProfiler"
	ProviderRuntime        = "Microsoft-Windows-DotNETRuntime"
	providerRundown        = "Microsoft-Windows-DotNETRuntimeRundown"

	KeywordLoader uint64 = 0x8  // module and assembly load events
	KeywordJit    uint64 = 0x10 // method load events
)

var (
	// ErrNotNettrace means the stream does not start with the NetTrace magic.
	ErrNotNettrace = errors.New("nettrace: not a nettrace stream")
	// ErrUnsupportedVersion means the stream is a NetTrace this reader does not read: a V6 file, a Trace object
	// that requires a newer reader, or a stream that does not begin with a Trace object.
	ErrUnsupportedVersion = errors.New("nettrace: unsupported nettrace format version")
	// ErrNoEvents means the stream holds no blocks at all, so there is nothing to read.
	ErrNoEvents = errors.New("nettrace: stream has no event or stack blocks")
)

// Frame is a resolved instruction in a sampled stack. A frame whose address no method load event covers keeps its
// IP and has empty names.
type Frame struct {
	IP       uint64
	Method   string // the method name
	Type     string // the declaring type's full name
	Assembly string // the fully qualified assembly name
}

// Sample is one thread sample.
type Sample struct {
	Timestamp uint64 // nanoseconds on the trace clock
	ThreadID  uint64
	// Managed is true when the thread was running managed code, false when it was in native code.
	Managed bool
	// Frames is the stack, innermost first. Samples on one stack share this slice, so callers must not modify it.
	Frames []Frame
}

// Method is a method-load record that sampled instruction addresses resolve against.
type Method struct {
	StartAddress uint64
	Size         uint64
	Namespace    string
	Name         string
	ModuleID     uint64
	Assembly     string
}

// Trace is the samples and symbols of a nettrace stream.
type Trace struct {
	TimestampFrequency uint64 // the trace clock's ticks per second
	PointerSize        int
	Samples            []Sample // ordered by timestamp
	Methods            []Method
	Assemblies         map[uint64]string // assembly ID to fully qualified name
	Modules            map[uint64]uint64 // module ID to assembly ID
}

// Parse reads a NetTrace V4/V5 stream, the format the runtime streams over its diagnostics IPC and dotnet-trace
// writes. It reads the whole stream into memory: wrap an untrusted reader in io.LimitReader.
func Parse(r io.Reader) (*Trace, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("Nettrace")) {
		return nil, ErrNotNettrace
	}

	trace := &Trace{
		Assemblies: make(map[uint64]string),
		Modules:    make(map[uint64]uint64),
	}
	metadata := make(map[uint32]eventMetadata)
	var events []rawEvent
	if err := parseV5(data, trace, metadata, &events); err != nil {
		return nil, err
	}

	slices.SortStableFunc(events, func(a, b rawEvent) int { return cmp.Compare(a.timestamp, b.timestamp) })
	// Samples on the same stack share one frame slice: a trace has far more samples than distinct stacks.
	shared := make(map[*uint64][]Frame)
	for _, event := range events {
		meta := metadata[event.metadata]
		if meta.provider != ProviderSampleProfiler || meta.eventID != threadSampleID {
			decodeRuntimeEvent(trace, meta.provider, meta.eventID, event.payload)
			continue
		}
		var frames []Frame
		if ips := event.frames; len(ips) > 0 {
			var ok bool
			if frames, ok = shared[&ips[0]]; !ok {
				frames = make([]Frame, len(ips))
				for i, ip := range ips {
					frames[i] = Frame{IP: ip}
				}
				shared[&ips[0]] = frames
			}
		}
		trace.Samples = append(trace.Samples, Sample{
			Timestamp: normalizeTimestamp(event.timestamp, trace.TimestampFrequency),
			ThreadID:  event.threadID,
			Managed:   len(event.payload) >= 4 && binary.LittleEndian.Uint32(event.payload) == threadSampleManaged,
			Frames:    frames,
		})
	}
	resolveMethods(trace)
	return trace, nil
}

// ThreadSample is event 0 of the sample profiler; its payload is 1 for a thread in native code, 2 for managed.
const (
	threadSampleID      = 0
	threadSampleManaged = 2
)

func normalizeTimestamp(value, frequency uint64) uint64 {
	if frequency == 0 || frequency == 1_000_000_000 {
		return value
	}
	return uint64((float64(value) * 1_000_000_000) / float64(frequency))
}
