package nettrace

import (
	"encoding/binary"
	"unicode/utf16"
)

// eventMetadata is what a metadata record names about an event: which provider emits it and under which ID.
type eventMetadata struct {
	provider string
	eventID  uint32
}

type rawEvent struct {
	timestamp uint64
	threadID  uint64
	metadata  uint32
	stackID   uint64
	payload   []byte
	// frames is the event's stack, resolved in file order against the stack blocks seen since the last sequence point.
	frames []uint64
}

type stackRecord struct {
	id     uint64
	frames []uint64
}

type v5EventState struct {
	metadataID    uint32
	sequence      uint64
	captureThread uint64
	processor     uint64
	thread        uint64
	stack         uint64
	timestamp     uint64
	payloadSize   uint64
}

func parseV5Events(data []byte, metadata map[uint32]eventMetadata, metadataBlock, compressed bool) []rawEvent {
	var out []rawEvent
	state := v5EventState{}
	if compressed {
		for offset := 0; offset < len(data); {
			event, used, ok := parseCompressedV5Event(data[offset:], &state)
			if !ok || used <= 0 {
				break
			}
			if metadataBlock {
				parseV5Metadata(event.payload, metadata)
			} else if event.metadata != 0 {
				out = append(out, event)
			}
			offset += used
		}
		return out
	}
	for offset := 0; offset+4 <= len(data); {
		eventSize := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		if eventSize <= 0 || offset+4+eventSize > len(data) {
			break
		}
		event, ok := parseUncompressedV5Event(data[offset+4:offset+4+eventSize], &state)
		if ok {
			if metadataBlock {
				parseV5Metadata(event.payload, metadata)
			} else if event.metadata != 0 {
				out = append(out, event)
			}
		}
		offset += (4 + eventSize + 3) &^ 3
	}
	return out
}

func parseCompressedV5Event(body []byte, state *v5EventState) (rawEvent, int, bool) {
	if len(body) < 1 {
		return rawEvent{}, 0, false
	}
	flags := body[0]
	offset := 1
	read := func() (uint64, bool) {
		value, used := readVarint(body[offset:])
		if used == 0 {
			return 0, false
		}
		offset += used
		return value, true
	}
	if flags&1 != 0 {
		value, ok := read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.metadataID = uint32FromUint64(value)
	}
	if flags&2 != 0 {
		value, ok := read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.sequence += value
		value, ok = read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.captureThread = value
		value, ok = read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.processor = value
	}
	if flags&4 != 0 {
		value, ok := read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.thread = value
	}
	if flags&8 != 0 {
		value, ok := read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.stack = value
	}
	value, ok := read()
	if !ok {
		return rawEvent{}, 0, false
	}
	state.timestamp += value
	if flags&16 != 0 {
		if offset+16 > len(body) {
			return rawEvent{}, 0, false
		}
		offset += 16
	}
	if flags&32 != 0 {
		if offset+16 > len(body) {
			return rawEvent{}, 0, false
		}
		offset += 16
	}
	if flags&128 != 0 {
		value, ok = read()
		if !ok {
			return rawEvent{}, 0, false
		}
		state.payloadSize = value
	}
	if state.payloadSize > uint64FromInt(len(body)-offset) {
		return rawEvent{}, 0, false
	}
	payloadEnd := offset + intFromUint64(state.payloadSize)
	payload := body[offset:payloadEnd]
	return rawEvent{timestamp: state.timestamp, threadID: state.thread, metadata: state.metadataID, stackID: state.stack, payload: payload}, payloadEnd, true
}

func parseUncompressedV5Event(body []byte, state *v5EventState) (rawEvent, bool) {
	if len(body) < 4+4+8+8+4+4+8+16+16+4 {
		return rawEvent{}, false
	}
	offset := 0
	// The runtime sets the high bit on the metadata id of an unsorted event.
	metadataID := binary.LittleEndian.Uint32(body[offset:]) &^ (1 << 31)
	offset += 4
	offset += 4
	thread := binary.LittleEndian.Uint64(body[offset:])
	offset += 8
	offset += 8
	offset += 4
	stack := uint64(binary.LittleEndian.Uint32(body[offset:]))
	offset += 4
	timestamp := binary.LittleEndian.Uint64(body[offset:])
	offset += 8
	offset += 16 + 16
	payloadSize := int(binary.LittleEndian.Uint32(body[offset:]))
	offset += 4
	if payloadSize < 0 || offset+payloadSize > len(body) {
		return rawEvent{}, false
	}
	state.metadataID = metadataID
	state.thread = thread
	state.stack = stack
	state.timestamp = timestamp
	return rawEvent{timestamp: timestamp, threadID: thread, metadata: metadataID, stackID: stack, payload: body[offset : offset+payloadSize]}, true
}

// parseV5Metadata records the provider and event ID of a metadata record. The event name and the field layouts that
// follow are not read: events are recognised by (provider, ID), which is how the runtime manifests number them.
func parseV5Metadata(payload []byte, metadata map[uint32]eventMetadata) {
	cursor := 0
	metadataID, ok := readUint32(payload, &cursor)
	if !ok {
		return
	}
	provider, ok := readWideNull(payload, &cursor)
	if !ok {
		return
	}
	eventID, ok := readUint32(payload, &cursor)
	if !ok {
		return
	}
	metadata[metadataID] = eventMetadata{provider: provider, eventID: eventID}
}

func parseV5StackBlock(block []byte, pointerSize int) []stackRecord {
	if len(block) < 8 {
		return nil
	}
	cursor := 0
	firstID := uint64(binary.LittleEndian.Uint32(block[cursor:]))
	cursor += 4
	count := intFromUint64(uint64(binary.LittleEndian.Uint32(block[cursor:])))
	cursor += 4
	// Every stack takes at least its 4-byte size, so a larger count cannot be real.
	if count < 0 || count > 1_000_000 || count > (len(block)-cursor)/4 {
		return nil
	}
	if pointerSize != 4 && pointerSize != 8 {
		pointerSize = 8
	}
	out := make([]stackRecord, 0, count)
	for index := range count {
		if cursor+4 > len(block) {
			return nil
		}
		stackSize := intFromUint64(uint64(binary.LittleEndian.Uint32(block[cursor:])))
		cursor += 4
		if stackSize < 0 || cursor+stackSize > len(block) || stackSize%pointerSize != 0 {
			return nil
		}
		frames := make([]uint64, stackSize/pointerSize)
		for frameIndex := range frames {
			if pointerSize == 4 {
				frames[frameIndex] = uint64(binary.LittleEndian.Uint32(block[cursor:]))
			} else {
				frames[frameIndex] = binary.LittleEndian.Uint64(block[cursor:])
			}
			cursor += pointerSize
		}
		out = append(out, stackRecord{id: firstID + uint64FromInt(index), frames: frames})
	}
	return out
}

func readUint32(data []byte, offset *int) (uint32, bool) {
	if *offset+4 > len(data) {
		return 0, false
	}
	value := binary.LittleEndian.Uint32(data[*offset:])
	*offset += 4
	return value, true
}

func readWideNull(data []byte, offset *int) (string, bool) {
	start := *offset
	for *offset+2 <= len(data) {
		unit := binary.LittleEndian.Uint16(data[*offset:])
		*offset += 2
		if unit == 0 {
			raw := make([]uint16, 0, (*offset-start)/2-1)
			for cursor := start; cursor+2 < *offset; cursor += 2 {
				raw = append(raw, binary.LittleEndian.Uint16(data[cursor:]))
			}
			return string(utf16.Decode(raw)), true
		}
	}
	return "", false
}

func align4(value int) int {
	return (value + 3) &^ 3
}

func parseV5EventBlock(block []byte, metadata map[uint32]eventMetadata, metadataBlock bool) []rawEvent {
	if len(block) < 20 {
		return nil
	}
	headerSize := int(binary.LittleEndian.Uint16(block[:2]))
	blockFlags := binary.LittleEndian.Uint16(block[2:4])
	if headerSize < 20 || headerSize > len(block) {
		return nil
	}
	compressed := blockFlags&1 != 0
	return parseV5Events(block[headerSize:], metadata, metadataBlock, compressed)
}
