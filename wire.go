package nettrace

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// The Diagnostic IPC protocol: a 20-byte header (magic, size, command set, command, reserved) and a payload.
const (
	headerSize             = 20
	commandSetEventPipe    = 0x02
	commandStopTracing     = 0x01
	commandCollectTracing2 = 0x03 // CollectTracing2: adds the rundown flag and per-provider filter data
	commandSetServer       = 0xFF // the runtime's replies
	replyOK                = 0x00
	replyError             = 0xFF
	formatNetTrace         = 1
)

var diagnosticMagic = [14]byte{'D', 'O', 'T', 'N', 'E', 'T', '_', 'I', 'P', 'C', '_', 'V', '1'}

// RuntimeError is the runtime's error reply to a request, such as a refused session or a provider it rejects.
type RuntimeError struct {
	// HResult is the runtime's failure code, zero when the reply carried none.
	HResult uint32
}

func (e *RuntimeError) Error() string {
	if e.HResult == 0 {
		return "nettrace: the runtime refused the request"
	}
	return fmt.Sprintf("nettrace: the runtime refused the request (HRESULT 0x%08X)", e.HResult)
}

func collectPayload(cfg Config) []byte {
	var payload []byte
	payload = binary.LittleEndian.AppendUint32(payload, cfg.BufferMB)
	payload = binary.LittleEndian.AppendUint32(payload, formatNetTrace)
	if cfg.NoRundown {
		payload = append(payload, 0)
	} else {
		payload = append(payload, 1)
	}
	payload = binary.LittleEndian.AppendUint32(payload, uint32Length(len(cfg.Providers)))
	for _, provider := range cfg.Providers {
		payload = binary.LittleEndian.AppendUint64(payload, provider.Keywords)
		payload = binary.LittleEndian.AppendUint32(payload, provider.Level)
		payload = appendWideString(payload, provider.Name)
		payload = appendWideString(payload, provider.Arguments)
	}
	return payload
}

// appendWideString writes a UTF-16 string as its code-unit count (including the terminator), the units and a NUL.
func appendWideString(payload []byte, value string) []byte {
	encoded := utf16.Encode([]rune(value))
	payload = binary.LittleEndian.AppendUint32(payload, uint32Length(len(encoded)+1))
	for _, unit := range encoded {
		payload = binary.LittleEndian.AppendUint16(payload, unit)
	}
	return append(payload, 0, 0)
}

func uint32Length(value int) uint32 {
	return uint32FromUint64(uint64FromInt(value))
}

func writeMessage(conn io.Writer, commandSet, commandID byte, payload []byte) error {
	size := headerSize + len(payload)
	if size > 0xffff {
		return errors.New("nettrace: diagnostics message is too large")
	}
	// One write per frame: a peer that has read the whole frame may hang up, and a second write would then fail.
	frame := make([]byte, headerSize, size)
	copy(frame[:14], diagnosticMagic[:])
	binary.LittleEndian.PutUint16(frame[14:16], uint16(size))
	frame[16] = commandSet
	frame[17] = commandID
	_, err := conn.Write(append(frame, payload...))
	return err
}

// readFrame reads one IPC message: its command set, command and payload.
func readFrame(conn io.Reader) (byte, byte, []byte, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, 0, nil, err
	}
	if string(header[:14]) != string(diagnosticMagic[:]) {
		return 0, 0, nil, errors.New("nettrace: invalid diagnostics response magic")
	}
	size := int(binary.LittleEndian.Uint16(header[14:16]))
	if size < headerSize {
		return 0, 0, nil, errors.New("nettrace: invalid diagnostics response size")
	}
	payload := make([]byte, size-headerSize)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return 0, 0, nil, err
	}
	return header[16], header[17], payload, nil
}

// readReply reads the runtime's reply to a request. Only an OK reply is a success: an error reply becomes a
// *RuntimeError, so a refused session is never mistaken for one that started.
func readReply(conn io.Reader) ([]byte, error) {
	set, command, payload, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	if set != commandSetServer {
		return nil, fmt.Errorf("nettrace: unexpected reply from command set 0x%02X", set)
	}
	switch command {
	case replyOK:
		return payload, nil
	case replyError:
		runtimeErr := &RuntimeError{}
		if len(payload) >= 4 {
			runtimeErr.HResult = binary.LittleEndian.Uint32(payload)
		}
		return nil, runtimeErr
	default:
		return nil, fmt.Errorf("nettrace: unexpected reply 0x%02X", command)
	}
}
