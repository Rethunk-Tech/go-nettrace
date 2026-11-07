//go:build !windows

package nettrace

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// A socket reached through a link, or anything that is not a socket, is not the runtime's: another user of a
// shared temp directory can plant either under the runtime's name.
func TestEndpointsThatAreNotOwnSocketsAreRefused(t *testing.T) {
	listener := listenAsRuntime(t)
	dir := filepath.Dir(listener.Addr().String())
	prefix := "dotnet-diagnostic-" + strconv.Itoa(os.Getpid()) + "-"
	if err := os.Remove(listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Bool
	planted := filepath.Join(dir, "planted")
	var listenConfig net.ListenConfig
	target, err := listenConfig.Listen(t.Context(), "unix", planted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		if conn, err := target.Accept(); err == nil {
			accepted.Store(true)
			_ = conn.Close()
		}
	}()
	if err := os.Symlink(planted, filepath.Join(dir, prefix+"0-socket")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefix+"1-socket"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = StartTracing(t.Context(), os.Getpid(), Config{}, io.Discard)
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("err = %v, want ErrNoEndpoint", err)
	}
	time.Sleep(50 * time.Millisecond)
	if accepted.Load() {
		t.Fatal("the library dialed a socket reached through a link")
	}
}

// serveSession answers the start handshake and then stream bytes, and answers one stop request when asked to.
func serveSession(t *testing.T, stream []byte) (stopped <-chan struct{}) {
	t.Helper()
	listener := listenAsRuntime(t)
	done := make(chan struct{})
	go func() {
		first, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = first.Close() }()
		if _, _, _, err := readFrame(first); err != nil {
			return
		}
		_ = reply(first, binary.LittleEndian.AppendUint64(nil, 41))
		go func() { _, _ = first.Write(stream) }()
		second, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = second.Close() }()
		if _, _, _, err := readFrame(second); err == nil {
			_ = reply(second, nil)
		}
		close(done)
	}()
	return done
}

func TestStreamPastTheByteCapIsRefused(t *testing.T) {
	serveSession(t, make([]byte, 1<<20))
	var w countingWriter
	session, err := StartTracing(t.Context(), os.Getpid(), Config{MaxStreamBytes: 1000}, &w)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := session.Stop(ctx); !errors.Is(err, ErrStreamTooLarge) {
		t.Fatalf("Stop = %v, want ErrStreamTooLarge", err)
	}
	if w.count() != 1000 {
		t.Fatalf("writer holds %d bytes, want the cap 1000", w.count())
	}
}

func TestStopSurvivesCanceledFirst(t *testing.T) {
	stopped := serveSession(t, []byte("Nettrace"))
	session, err := StartTracing(t.Context(), os.Getpid(), Config{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_ = session.Stop(canceled)
	ctx, cancelLive := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelLive()
	// The canceled first call closed the session's connection, so the copy error is expected; the stop is not.
	if err := session.Stop(ctx); errors.Is(err, context.Canceled) {
		t.Fatalf("second Stop = %v, the first caller's cancellation leaked into the shared stop", err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the stop request never reached the endpoint")
	}
}
