//go:build windows

package nettrace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipe     = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
)

// TestPipeDisconnectIsEOF ends a pipe the way the runtime ends a session (write, flush, disconnect) and checks the
// client reads the data and then a clean end, not ERROR_PIPE_NOT_CONNECTED.
func TestPipeDisconnectIsEOF(t *testing.T) {
	t.Parallel()
	name := fmt.Sprintf(`\\.\pipe\go-nettrace-test-%d`, os.Getpid())
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	const pipeAccessDuplex, pipeTypeByte, unlimitedInstances = 3, 0, 255
	handle, _, callErr := procCreateNamedPipe.Call(uintptr(unsafe.Pointer(path)), pipeAccessDuplex, pipeTypeByte, //nolint:gosec // G103: CreateNamedPipeW takes the name as a UTF-16 pointer
		unlimitedInstances, 4096, 4096, 0, 0)
	server := syscall.Handle(handle)
	if server == syscall.InvalidHandle {
		t.Fatal(callErr)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(server) })

	conn, err := openEndpoint(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	const errPipeConnected = syscall.Errno(535)
	if ok, _, callErr := procConnectNamedPipe.Call(uintptr(server), 0); ok == 0 && !errors.Is(callErr, errPipeConnected) {
		t.Fatal(callErr)
	}
	// FlushFileBuffers waits until the client has read everything, so the server side runs beside the read.
	served := make(chan error, 1)
	go func() {
		if _, err := syscall.Write(server, []byte("stream")); err != nil {
			served <- err
			return
		}
		if err := syscall.FlushFileBuffers(server); err != nil {
			served <- err
			return
		}
		if ok, _, callErr := procDisconnectNamedPipe.Call(uintptr(server)); ok == 0 {
			served <- callErr
			return
		}
		served <- nil
	}()

	// io.Copy, as the session's stream copy uses, so a WriterTo on the connection cannot bypass its Read.
	var got bytes.Buffer
	if _, err := io.Copy(&got, conn); err != nil || got.String() != "stream" {
		t.Errorf("read %q, %v; want %q and a clean end", got.String(), err, "stream")
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
