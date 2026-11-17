package nettrace

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	trace := readFixture(f)
	f.Add(trace)
	f.Add(trace[:len(trace)/2])
	f.Add(trace[:200])
	f.Add([]byte("Nettrace"))
	mangled := bytes.Clone(trace[:16<<10])
	copy(mangled[12:32], strings.Repeat("x", 20))
	f.Add(mangled)
	zeroed := bytes.Clone(trace[:16<<10])
	clear(zeroed[8:64])
	f.Add(zeroed)
	f.Add([]byte("not a nettrace"))
	f.Add(append([]byte("Nettrace"), bytes.Repeat([]byte{0}, 64)...))
	f.Add(append([]byte("Nettrace\x14\x00\x00\x00!FastSerialization.1"), bytes.Repeat([]byte{0}, 64)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Parse(bytes.NewReader(data))
		if err != nil {
			if got != nil {
				t.Fatal("Parse returned a trace with an error")
			}
			return
		}
		if got == nil {
			t.Fatal("Parse returned nil without an error")
		}
		if got.Assemblies == nil || got.Modules == nil {
			t.Fatal("Parse returned nil maps")
		}
		if len(got.Samples) > len(data) || len(got.Methods) > len(data) ||
			len(got.Assemblies) > len(data) || len(got.Modules) > len(data) {
			t.Fatal("Parse allocated more records than the stream has bytes")
		}
		for i := 1; i < len(got.Samples); i++ {
			if got.Samples[i].Timestamp < got.Samples[i-1].Timestamp {
				t.Fatalf("samples not ordered by timestamp at %d", i)
			}
		}
		for _, method := range got.Methods {
			if method.StartAddress == 0 || method.Size == 0 {
				t.Fatal("Parse kept a method with a zero range")
			}
		}
		for id := range got.Assemblies {
			if id == 0 {
				t.Fatal("Parse kept assembly ID 0")
			}
		}
		for module, assembly := range got.Modules {
			if module == 0 || assembly == 0 {
				t.Fatal("Parse kept a zero module or assembly ID")
			}
		}
	})
}

func addIPCFrame(f *testing.F, set, command byte, payload []byte) {
	f.Helper()
	var buf bytes.Buffer
	if err := writeMessage(&buf, set, command, payload); err != nil {
		f.Fatal(err)
	}
	f.Add(buf.Bytes())
}

func FuzzReadFrame(f *testing.F) {
	addIPCFrame(f, commandSetServer, replyOK, binary.LittleEndian.AppendUint64(nil, 41))
	addIPCFrame(f, commandSetServer, replyError, []byte{0x05, 0x00, 0x07, 0x80})
	addIPCFrame(f, commandSetServer, replyOK, nil)
	addIPCFrame(f, commandSetEventPipe, replyOK, []byte("x"))
	addIPCFrame(f, commandSetServer, 0x01, nil)
	f.Add([]byte{})
	f.Add(diagnosticMagic[:])
	empty := make([]byte, headerSize)
	copy(empty, diagnosticMagic[:])
	binary.LittleEndian.PutUint16(empty[14:16], headerSize)
	f.Add(empty)
	tooSmall := make([]byte, headerSize)
	copy(tooSmall, diagnosticMagic[:])
	binary.LittleEndian.PutUint16(tooSmall[14:16], 10)
	f.Add(tooSmall)
	f.Fuzz(func(t *testing.T, data []byte) {
		set, command, payload, err := readFrame(bytes.NewReader(data))
		if err != nil {
			return
		}
		if len(payload) > 0xffff-headerSize {
			t.Fatalf("payload is %d bytes, beyond a uint16 size field", len(payload))
		}
		var buf bytes.Buffer
		if err := writeMessage(&buf, set, command, payload); err != nil {
			t.Fatalf("writeMessage rejected a frame readFrame accepted: %v", err)
		}
		set2, command2, payload2, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("round-trip read: %v", err)
		}
		if set2 != set || command2 != command || !bytes.Equal(payload2, payload) {
			t.Fatal("readFrame round-trip mismatch")
		}
	})
}

func FuzzReadReply(f *testing.F) {
	addIPCFrame(f, commandSetServer, replyOK, binary.LittleEndian.AppendUint64(nil, 41))
	addIPCFrame(f, commandSetServer, replyError, []byte{0x05, 0x00, 0x07, 0x80})
	addIPCFrame(f, commandSetServer, replyOK, nil)
	addIPCFrame(f, commandSetServer, replyError, nil)
	addIPCFrame(f, commandSetEventPipe, replyOK, nil)
	addIPCFrame(f, commandSetServer, 0x02, []byte{1})
	f.Add([]byte{})
	f.Add(diagnosticMagic[:])
	f.Fuzz(func(t *testing.T, data []byte) {
		payload, err := readReply(bytes.NewReader(data))
		if err == nil {
			if payload == nil {
				t.Fatal("readReply returned nil payload without an error")
			}
			return
		}
		runtimeErr, ok := errors.AsType[*RuntimeError](err)
		if !ok {
			return
		}
		set, command, framePayload, frameErr := readFrame(bytes.NewReader(data))
		if frameErr != nil {
			t.Fatal("readReply returned RuntimeError but readFrame failed")
		}
		if set != commandSetServer || command != replyError {
			t.Fatal("RuntimeError from a frame that is not an error reply")
		}
		if len(framePayload) >= 4 {
			if runtimeErr.HResult != binary.LittleEndian.Uint32(framePayload) {
				t.Fatal("HResult does not match the payload")
			}
		} else if runtimeErr.HResult != 0 {
			t.Fatal("HResult set without four payload bytes")
		}
	})
}

func FuzzDecodeRuntimeEvent(f *testing.F) {
	module := binary.LittleEndian.AppendUint64(nil, 0x10)
	module = binary.LittleEndian.AppendUint64(module, 0x20)
	f.Add(ProviderRuntime, uint32(154), module)
	f.Add(providerRundown, uint32(154), module)
	f.Add(ProviderRuntime, uint32(143), make([]byte, 24))
	f.Add(ProviderRuntime, uint32(152), module)
	f.Add(providerRundown, uint32(156), make([]byte, 32))
	f.Add("", uint32(0), []byte{})
	f.Fuzz(func(t *testing.T, provider string, eventID uint32, payload []byte) {
		trace := &Trace{Assemblies: map[uint64]string{}, Modules: map[uint64]uint64{}}
		decodeRuntimeEvent(trace, provider, eventID, payload)
		if len(trace.Methods) > 1 || len(trace.Assemblies) > 1 || len(trace.Modules) > 1 {
			t.Fatal("one event produced more than one record")
		}
		for _, method := range trace.Methods {
			if method.StartAddress == 0 || method.Size == 0 {
				t.Fatal("kept a method with a zero range")
			}
		}
		for id, name := range trace.Assemblies {
			if id == 0 || name == "" {
				t.Fatal("kept an empty assembly")
			}
		}
		for moduleID, assemblyID := range trace.Modules {
			if moduleID == 0 || assemblyID == 0 {
				t.Fatal("kept a zero module mapping")
			}
		}
	})
}

func FuzzParseV5StackBlock(f *testing.F) {
	block := binary.LittleEndian.AppendUint32(nil, 4)
	block = binary.LittleEndian.AppendUint32(block, 1)
	block = binary.LittleEndian.AppendUint32(block, 16)
	block = binary.LittleEndian.AppendUint64(block, 0x1000)
	block = binary.LittleEndian.AppendUint64(block, 0x2000)
	f.Add(block, 8)
	narrow := binary.LittleEndian.AppendUint32(nil, 0)
	narrow = binary.LittleEndian.AppendUint32(narrow, 1)
	narrow = binary.LittleEndian.AppendUint32(narrow, 8)
	narrow = binary.LittleEndian.AppendUint32(narrow, 0x1000)
	narrow = binary.LittleEndian.AppendUint32(narrow, 0x2000)
	f.Add(narrow, 4)
	lying := binary.LittleEndian.AppendUint32(nil, 0)
	lying = binary.LittleEndian.AppendUint32(lying, 1_000_000)
	f.Add(lying, 8)
	f.Add([]byte{}, 8)
	f.Fuzz(func(t *testing.T, block []byte, pointerSize int) {
		stacks := parseV5StackBlock(block, pointerSize)
		if len(stacks) > len(block) {
			t.Fatal("more stacks than the block has bytes")
		}
		size := pointerSize
		if size != 4 && size != 8 {
			size = 8
		}
		for _, stack := range stacks {
			if len(stack.frames)*size > len(block) {
				t.Fatal("a stack's frames exceed the block")
			}
		}
	})
}

func FuzzParseCompressedV5Event(f *testing.F) {
	f.Add([]byte{0x88, 3, 10, 2, 0xaa, 0xbb})
	f.Add([]byte{0})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, body []byte) {
		state := v5EventState{}
		event, used, ok := parseCompressedV5Event(body, &state)
		if !ok {
			if used != 0 {
				t.Fatal("failed parse returned a non-zero used count")
			}
			return
		}
		if used <= 0 || used > len(body) {
			t.Fatalf("used %d for %d-byte body", used, len(body))
		}
		if len(event.payload) > used {
			t.Fatal("payload longer than the bytes consumed")
		}
	})
}

func FuzzParseV5Metadata(f *testing.F) {
	payload := binary.LittleEndian.AppendUint32(nil, 7)
	payload = appendWideForTest(payload, "Provider")
	payload = binary.LittleEndian.AppendUint32(payload, 143)
	payload = appendWideForTest(payload, "MethodLoadVerbose")
	f.Add(payload)
	f.Add(payload[:len(payload)-2])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		metadata := map[uint32]eventMetadata{}
		parseV5Metadata(payload, metadata)
		if len(metadata) > 1 {
			t.Fatal("one record produced more than one metadata entry")
		}
	})
}
