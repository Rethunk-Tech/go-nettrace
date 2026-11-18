//go:build !windows

package nettrace

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// maxUnixSocketPath is sun_path's 108 bytes less its terminating NUL.
const maxUnixSocketPath = 107

var errUnexpectedCommand = io.ErrUnexpectedEOF

func reply(conn io.Writer, payload []byte) error {
	return writeMessage(conn, commandSetServer, replyOK, payload)
}

// listenAsRuntime makes this process look like a .NET process with a diagnostics socket.
func listenAsRuntime(t *testing.T) net.Listener {
	t.Helper()
	// t.TempDir() lives under the worktree, which is already longer than sun_path; os.TempDir is the short scratch root.
	temp := filepath.Join(os.TempDir(), "n"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Mkdir(temp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	socket := filepath.Join(temp, "dotnet-diagnostic-"+strconv.Itoa(os.Getpid())+"-test-socket")
	if len(socket) > maxUnixSocketPath {
		t.Skipf("socket path is %d bytes, over the %d a unix socket holds; run with a shorter TMPDIR", len(socket), maxUnixSocketPath)
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
		_ = os.Remove(temp)
	})
	return listener
}

func TestSessionUsesDiagnosticsSocket(t *testing.T) {
	listener := listenAsRuntime(t)
	serverErr := make(chan error, 1)
	var collect []byte
	go func() {
		first, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		set, command, payload, err := readFrame(first)
		collect = payload
		if err == nil && (set != commandSetEventPipe || command != commandCollectTracing2) {
			err = errUnexpectedCommand
		}
		if err == nil {
			err = reply(first, binary.LittleEndian.AppendUint64(nil, 41))
		}
		if err == nil {
			_, err = first.Write([]byte("Nettrace"))
		}
		if err != nil {
			_ = first.Close()
			serverErr <- err
			return
		}
		second, err := listener.Accept()
		if err != nil {
			_ = first.Close()
			serverErr <- err
			return
		}
		_, stopCommand, stopPayload, err := readFrame(second)
		if err == nil && (stopCommand != commandStopTracing || binary.LittleEndian.Uint64(stopPayload) != 41) {
			err = errUnexpectedCommand
		}
		if err == nil {
			err = reply(second, nil)
		}
		_ = second.Close()
		_ = first.Close()
		serverErr <- err
	}()

	var out bytes.Buffer
	session, err := StartTracing(t.Context(), os.Getpid(), Config{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Nettrace" {
		t.Fatalf("stream = %q, want %q", out.String(), "Nettrace")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake diagnostics server did not finish")
	}
	for _, want := range []string{ProviderSampleProfiler, ProviderRuntime} {
		if !bytes.Contains(collect, wide(want)) {
			t.Errorf("CollectTracing2 payload lacks provider %s", want)
		}
	}
}

func wide(s string) []byte {
	var out []byte
	for _, unit := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, unit)
	}
	return out
}

func TestCollectPayloadEncodesConfig(t *testing.T) {
	t.Parallel()
	cfg := Config{BufferMB: 64, NoRundown: true, Providers: []Provider{{Name: "P", Keywords: 0x18, Arguments: "a=b"}}}.withDefaults()
	got := collectPayload(cfg)
	want := binary.LittleEndian.AppendUint32(nil, 64)
	want = binary.LittleEndian.AppendUint32(want, 1) // NetTrace format
	want = append(want, 0)                           // no rundown
	want = binary.LittleEndian.AppendUint32(want, 1)
	want = binary.LittleEndian.AppendUint64(want, 0x18)
	want = binary.LittleEndian.AppendUint32(want, 5) // zero level selects verbose
	want = binary.LittleEndian.AppendUint32(want, 2)
	want = append(want, wide("P")...)
	want = append(want, 0, 0)
	want = binary.LittleEndian.AppendUint32(want, 4)
	want = append(want, wide("a=b")...)
	want = append(want, 0, 0)
	if !bytes.Equal(got, want) {
		t.Fatalf("payload = %x\nwant      %x", got, want)
	}
}

func TestAnErrorReplyIsAnError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeMessage(&buf, commandSetServer, replyError, []byte{0x05, 0x00, 0x07, 0x80}); err != nil {
		t.Fatal(err)
	}
	_, err := readReply(&buf)
	if runtimeErr, ok := errors.AsType[*RuntimeError](err); !ok || runtimeErr.HResult != 0x80070005 {
		t.Fatalf("error reply read as %v", err)
	}
}

func TestAReplyFromAnotherCommandSetIsRefused(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeMessage(&buf, commandSetEventPipe, replyOK, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readReply(&buf); err == nil {
		t.Fatal("a reply outside the server command set read as success")
	}
}

func TestNoEndpointIsTyped(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// pid 1<<30 is above any real pid_max.
	_, err := StartTracing(t.Context(), 1<<30, Config{}, io.Discard)
	if !errors.Is(err, ErrNoEndpoint) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNoEndpoint wrapping os.ErrNotExist", err)
	}
}

func TestAnEndpointGoneBeforeTheDialIsNoEndpoint(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	// A dangling link matches the glob but fails the dial with ENOENT, as a pipe or socket that is not there does.
	socket := filepath.Join(temp, "dotnet-diagnostic-"+strconv.Itoa(os.Getpid())+"-gone-socket")
	if err := os.Symlink(filepath.Join(temp, "missing"), socket); err != nil {
		t.Fatal(err)
	}
	_, err := StartTracing(t.Context(), os.Getpid(), Config{}, io.Discard)
	if !errors.Is(err, ErrNoEndpoint) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNoEndpoint wrapping os.ErrNotExist", err)
	}
}

// startSilentSession starts a session whose endpoint answers the handshake, streams out until closed, and then
// ignores every later connection.
func startSilentSession(t *testing.T, w io.Writer) *Session {
	t.Helper()
	listener := listenAsRuntime(t)
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
		go func() {
			for {
				if _, err := first.Write([]byte("x")); err != nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()
	session, err := StartTracing(t.Context(), os.Getpid(), Config{}, w)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestStopHonoursItsContextAndIsIdempotent(t *testing.T) {
	session := startSilentSession(t, io.Discard)
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		returned := make(chan error, 1)
		go func() { returned <- session.Stop(ctx) }()
		select {
		case err := <-returned:
			if err == nil {
				t.Fatal("Stop against a silent endpoint reported success")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Stop ignored its context")
		}
		cancel()
	}
}

func TestConcurrentStopHonoursItsOwnContext(t *testing.T) {
	session := startSilentSession(t, io.Discard)
	long, cancelLong := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelLong()
	go func() { _ = session.Stop(long) }()
	time.Sleep(100 * time.Millisecond)
	short, cancelShort := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelShort()
	start := time.Now()
	if err := session.Stop(short); err == nil {
		t.Fatal("second Stop against a silent endpoint reported success")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("second Stop took %v, ignoring its 100ms context", elapsed)
	}
}

// countingWriter counts bytes under a lock so a test can watch for writes after Stop returns.
type countingWriter struct {
	mu sync.Mutex
	n  int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += len(p)
	return len(p), nil
}

func (c *countingWriter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestNothingIsWrittenAfterStopReturns(t *testing.T) {
	var w countingWriter
	session := startSilentSession(t, &w)
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := session.Stop(ctx); err == nil {
		t.Fatal("Stop against a silent endpoint reported success")
	}
	after := w.count()
	if after == 0 {
		t.Fatal("the stream was never copied")
	}
	time.Sleep(100 * time.Millisecond)
	if w.count() != after {
		t.Fatalf("the writer received %d bytes after Stop returned", w.count()-after)
	}
}

func TestStartHonoursItsContext(t *testing.T) {
	listener := listenAsRuntime(t)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _, _, _ = readFrame(conn)
			time.Sleep(3 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := StartTracing(ctx, os.Getpid(), Config{}, io.Discard)
		returned <- err
	}()
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Start against a silent endpoint returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start ignored its context")
	}
}

func TestRuntimeErrorText(t *testing.T) {
	if got := (&RuntimeError{}).Error(); strings.Contains(got, "HRESULT") {
		t.Errorf("no code, yet %q", got)
	}
	if got := (&RuntimeError{HResult: 0x80070005}).Error(); !strings.Contains(got, "0x80070005") {
		t.Errorf("code missing from %q", got)
	}
}

func TestABrokenHandshakeNeverStartsASession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve func(net.Conn)
		check func(error) bool
	}{
		{
			name: "peer hangs up before a reply",
			serve: func(conn net.Conn) {
				_, _, _, _ = readFrame(conn)
				_ = conn.Close()
			},
			check: func(err error) bool { return err != nil },
		},
		{
			name: "reply has no session id",
			serve: func(conn net.Conn) {
				_, _, _, _ = readFrame(conn)
				_ = reply(conn, []byte{1, 2, 3, 4})
			},
			check: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "session id")
			},
		},
		{
			name: "runtime error reply",
			serve: func(conn net.Conn) {
				_, _, _, _ = readFrame(conn)
				_ = writeMessage(conn, commandSetServer, replyError, []byte{0x05, 0x00, 0x07, 0x80})
			},
			check: func(err error) bool {
				_, ok := errors.AsType[*RuntimeError](err)
				return ok
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := listenAsRuntime(t)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				tc.serve(conn)
			}()
			_, err := StartTracing(t.Context(), os.Getpid(), Config{}, io.Discard)
			if !tc.check(err) {
				t.Fatalf("StartTracing = %v", err)
			}
		})
	}
}

func TestABrokenStopClosesTheStream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve func(listener net.Listener, first net.Conn, readyForStop chan struct{})
		check func(error) bool
	}{
		{
			name: "second endpoint is gone",
			serve: func(listener net.Listener, first net.Conn, readyForStop chan struct{}) {
				_ = listener.Close()
				close(readyForStop)
				_, _ = io.Copy(io.Discard, first)
			},
			check: func(err error) bool { return errors.Is(err, ErrNoEndpoint) },
		},
		{
			name: "stop peer hangs up before a reply",
			serve: func(listener net.Listener, first net.Conn, readyForStop chan struct{}) {
				go func() { _, _ = io.Copy(io.Discard, first) }()
				close(readyForStop)
				second, err := listener.Accept()
				if err != nil {
					return
				}
				_ = second.Close()
			},
			check: func(err error) bool { return err != nil },
		},
		{
			name: "stop reply is a runtime error",
			serve: func(listener net.Listener, first net.Conn, readyForStop chan struct{}) {
				go func() { _, _ = io.Copy(io.Discard, first) }()
				close(readyForStop)
				second, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = second.Close() }()
				_, _, _, _ = readFrame(second)
				_ = writeMessage(second, commandSetServer, replyError, []byte{0x05, 0x00, 0x07, 0x80})
			},
			check: func(err error) bool {
				_, ok := errors.AsType[*RuntimeError](err)
				return ok
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := listenAsRuntime(t)
			ready := make(chan struct{})
			readyForStop := make(chan struct{})
			go func() {
				first, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = first.Close() }()
				if _, _, _, err := readFrame(first); err != nil {
					return
				}
				if err := reply(first, binary.LittleEndian.AppendUint64(nil, 41)); err != nil {
					return
				}
				close(ready)
				tc.serve(listener, first, readyForStop)
			}()
			session, err := StartTracing(t.Context(), os.Getpid(), Config{}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("handshake did not finish")
			}
			select {
			case <-readyForStop:
			case <-time.After(2 * time.Second):
				t.Fatal("stop fixture did not finish")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if err := session.Stop(ctx); !tc.check(err) {
				t.Fatalf("Stop = %v", err)
			}
		})
	}
}

func TestABytePastTheCapMarksTheStreamTooLarge(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	go func() {
		_, _ = server.Write([]byte("12345"))
		_ = server.Close()
	}()
	session := &Session{conn: client, maxBytes: 4, done: make(chan struct{})}
	var out bytes.Buffer
	session.copyStream(&out)
	if !errors.Is(session.streamErr, ErrStreamTooLarge) {
		t.Fatalf("streamErr = %v, want ErrStreamTooLarge", session.streamErr)
	}
	if out.String() != "1234" {
		t.Fatalf("writer = %q, want the four bytes inside the cap", out.String())
	}
}

func TestStopReturnsItsOwnCancelWhenTheSharedStopIsStillRunning(t *testing.T) {
	t.Parallel()
	session := &Session{
		conn: closedConn{},
		done: make(chan struct{}),
	}
	session.stopOnce.Do(func() {})
	session.stopDone = make(chan struct{})
	close(session.done)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := session.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop = %v, want context.Canceled", err)
	}
}

type closedConn struct{}

func (closedConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (closedConn) Write(p []byte) (int, error) { return len(p), nil }
func (closedConn) Close() error                { return nil }
