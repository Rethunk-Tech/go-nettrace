package nettrace

import (
	"encoding/binary"
)

type v5Block struct {
	kind       string
	dataStart  int
	blockStart int
	blockEnd   int
}

// traceFirstObject is where the Trace object starts: right after the 8-byte magic and the length-prefixed
// "!FastSerialization.1" tag.
const traceFirstObject = 32

// maxReaderVersion is the newest format version whose block layouts this reader knows.
const maxReaderVersion = 5

// parseV5 reads the Trace object and every block into trace and events. It returns ErrUnsupportedVersion for a
// stream that is not a FastSerialization NetTrace V4/V5 file, and ErrNoEvents for one with no blocks.
func parseV5(data []byte, trace *Trace, metadata map[uint32]eventMetadata, events *[]rawEvent) error {
	if len(data) < traceFirstObject || string(data[12:traceFirstObject]) != "!FastSerialization.1" {
		return ErrUnsupportedVersion
	}
	// Without the Trace object first there is no clock frequency or pointer size to read the rest by.
	if name, _, ok := v5ObjectType(data, traceFirstObject); !ok || name != "Trace" ||
		binary.LittleEndian.Uint32(data[traceFirstObject+7:]) > maxReaderVersion {
		return ErrUnsupportedVersion
	}
	blocks := make([]v5Block, 0)
	for offset := traceFirstObject; offset+15 < len(data); {
		name, typeEnd, ok := v5ObjectType(data, offset)
		if !ok {
			offset++
			continue
		}
		if name == "Trace" {
			parseTraceFields(data[typeEnd+1:], trace)
			offset = typeEnd + 1
			continue
		}
		block, ok := v5BlockBounds(data, typeEnd+1, name)
		if !ok {
			offset = typeEnd + 1
			continue
		}
		blocks = append(blocks, block)
		offset = block.blockEnd
		if offset < len(data) && data[offset] == 6 {
			offset++
		}
	}
	if len(blocks) == 0 {
		return ErrNoEvents
	}
	for _, block := range blocks {
		if block.kind == "MetadataBlock" {
			parseV5EventBlock(data[block.blockStart:block.blockEnd], metadata, true)
		}
	}
	// Stack IDs are only unique until the next sequence point, which flushes the stack cache and lets later stack
	// blocks reuse them, so each event's stack is resolved in file order against the stacks seen since then.
	current := make(map[uint64][]uint64)
	for _, block := range blocks {
		switch block.kind {
		case "SPBlock":
			clear(current)
		case "StackBlock":
			for _, stack := range parseV5StackBlock(data[block.blockStart:block.blockEnd], trace.PointerSize) {
				current[stack.id] = stack.frames
			}
		case "EventBlock":
			for _, event := range parseV5EventBlock(data[block.blockStart:block.blockEnd], metadata, false) {
				event.frames = current[event.stackID]
				*events = append(*events, event)
			}
		}
	}
	return nil
}

func v5ObjectType(data []byte, offset int) (string, int, bool) {
	if offset+15 > len(data) || data[offset] != 5 || data[offset+1] != 5 || data[offset+2] != 1 {
		return "", 0, false
	}
	nameLength := int(binary.LittleEndian.Uint32(data[offset+11 : offset+15]))
	nameStart := offset + 15
	nameEnd := nameStart + nameLength
	if nameLength < 0 || nameEnd >= len(data) || data[nameEnd] != 6 {
		return "", 0, false
	}
	name := string(data[nameStart:nameEnd])
	switch name {
	case "Trace", "EventBlock", "MetadataBlock", "StackBlock", "SPBlock":
		return name, nameEnd, true
	default:
		return "", 0, false
	}
}

func v5BlockBounds(data []byte, dataStart int, kind string) (v5Block, bool) {
	if dataStart+4 > len(data) {
		return v5Block{}, false
	}
	blockStart := align4(dataStart + 4)
	if blockStart+4 > len(data) {
		return v5Block{}, false
	}
	blockSize := int(binary.LittleEndian.Uint32(data[dataStart : dataStart+4]))
	blockEnd := blockStart + blockSize
	if blockSize < 8 || blockEnd > len(data) {
		return v5Block{}, false
	}
	return v5Block{kind: kind, dataStart: dataStart, blockStart: blockStart, blockEnd: blockEnd}, true
}

func parseTraceFields(data []byte, trace *Trace) {
	// The Trace object starts with a 16-byte SYSTEMTIME, then the sync QPC value and the QPC frequency.
	if len(data) < 16+8+8+4 {
		return
	}
	cursor := 16
	cursor += 8
	trace.TimestampFrequency = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	trace.PointerSize = int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
}
