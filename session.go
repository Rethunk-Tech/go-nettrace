package nettrace

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"
)

// DefaultBufferMB is the runtime-side event buffer a zero Config.BufferMB selects.
const DefaultBufferMB uint32 = 256

// defaultLevel is the verbose level, which the method-load events need.
const defaultLevel uint32 = 5

// Provider is one event provider to enable in a session.
type Provider struct {
	Name     string
	Keywords uint64
	// Level is the verbosity; zero selects 5 (verbose), which the method-load events need.
	Level uint32
	// Arguments is the provider's filter data as "key=value;..." pairs; empty sends none.
	Arguments string
}

// DefaultMaxStreamBytes is the stream size a zero Config.MaxStreamBytes selects.
const DefaultMaxStreamBytes int64 = 1 << 30

// ErrStreamTooLarge means the endpoint sent more than Config.MaxStreamBytes; the session's connection is closed and
// the writer holds a truncated stream.
var ErrStreamTooLarge = errors.New("nettrace: stream exceeds the byte cap")

// stopTimeout bounds the shared stop, which no single caller's context may cancel.
const stopTimeout = 30 * time.Second

// Config describes a tracing session. A zero field selects its default.
type Config struct {
	// Providers defaults to the sample profiler plus the runtime's loader and JIT events, which is what Parse
	// needs to resolve stacks.
	Providers []Provider
	BufferMB  uint32 // zero selects DefaultBufferMB
	// NoRundown skips the rundown the runtime performs when the session stops. Rundown is on by default: without
	// it, every method JIT-compiled before the session started stays unresolved.
	NoRundown bool
	// MaxStreamBytes caps the bytes copied to the writer, so an endpoint that never stops sending cannot fill the
	// disk; zero selects DefaultMaxStreamBytes.
	MaxStreamBytes int64
}

func (c Config) withDefaults() Config {
	if c.BufferMB == 0 {
		c.BufferMB = DefaultBufferMB
	}
	if c.MaxStreamBytes <= 0 {
		c.MaxStreamBytes = DefaultMaxStreamBytes
	}
	if len(c.Providers) == 0 {
		c.Providers = []Provider{
			{Name: ProviderSampleProfiler},
			{Name: ProviderRuntime, Keywords: KeywordLoader | KeywordJit},
		}
	}
	providers := make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		if p.Level == 0 {
			p.Level = defaultLevel
		}
		providers[i] = p
	}
	c.Providers = providers
	return c
}

// Session is one running EventPipe session.
type Session struct {
	pid       int
	maxBytes  int64
	conn      io.ReadWriteCloser
	sessionID uint64
	streamErr error
	done      chan struct{} // closed when the stream copy has ended
	stopOnce  sync.Once
	stopDone  chan struct{}
	stopErr   error
}

// StartTracing dials pid's diagnostics endpoint, sends CollectTracing2, and copies the nettrace stream into w until
// the runtime closes it. It returns an error wrapping ErrNoEndpoint while the process has no endpoint yet, so a
// caller may retry. A refused request is a *RuntimeError. Canceling ctx before the reply closes the connection and
// returns ctx.Err(). The stream is copied from a goroutine, so w must be safe to write while the caller does other
// work; it is not written after Stop returns.
func StartTracing(ctx context.Context, pid int, cfg Config, w io.Writer) (*Session, error) {
	cfg = cfg.withDefaults()
	conn, err := dialEndpoint(ctx, pid)
	if err != nil {
		return nil, err
	}
	// A silent endpoint would block the handshake reads forever; closing the connection unblocks them.
	unbind := context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(err error) (*Session, error) {
		unbind()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := writeMessage(conn, commandSetEventPipe, commandCollectTracing2, collectPayload(cfg)); err != nil {
		return fail(err)
	}
	payload, err := readReply(conn)
	if err != nil {
		return fail(err)
	}
	if len(payload) < 8 {
		return fail(errors.New("nettrace: diagnostics response has no session id"))
	}
	if !unbind() {
		return fail(ctx.Err())
	}
	session := &Session{
		pid:       pid,
		maxBytes:  cfg.MaxStreamBytes,
		conn:      conn,
		sessionID: binary.LittleEndian.Uint64(payload[:8]),
		done:      make(chan struct{}),
	}
	go session.copyStream(w)
	return session, nil
}

// Stop sends StopTracing on a second connection, waits for the stream to end, and returns the stop and copy errors
// joined. It may be called again and by several goroutines: the stop runs once, and every call waits for it under
// its own ctx. Canceling ctx closes the session connection and ends the wait, so Stop never outlives its context; the stop itself runs detached from every caller, under its own timeout. Stop returns only after
// the copy goroutine has exited: nothing is written to the writer after it returns, so the caller may read or close
// it at once.
func (s *Session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.stopDone = make(chan struct{})
		// The stop is shared, so it runs under no single caller's cancellation; each caller still waits under its own.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
		go func() {
			defer cancel()
			s.stopErr = s.stop(stopCtx)
			close(s.stopDone)
		}()
	})
	unbind := context.AfterFunc(ctx, func() { _ = s.conn.Close() })
	defer unbind()
	var stopErr error
	select {
	case <-s.stopDone:
		stopErr = s.stopErr
	case <-ctx.Done():
		stopErr = ctx.Err()
	}
	<-s.done
	return errors.Join(stopErr, s.streamErr)
}

func (s *Session) stop(ctx context.Context) error {
	conn, err := dialEndpoint(ctx, s.pid)
	if err != nil {
		_ = s.conn.Close()
		return err
	}
	unbind := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		unbind()
		_ = conn.Close()
	}()
	if err := writeMessage(conn, commandSetEventPipe, commandStopTracing, binary.LittleEndian.AppendUint64(nil, s.sessionID)); err != nil {
		_ = s.conn.Close()
		return err
	}
	if _, err = readReply(conn); err != nil {
		_ = s.conn.Close()
		return err
	}
	return nil
}

func (s *Session) copyStream(w io.Writer) {
	limited := &io.LimitedReader{R: s.conn, N: s.maxBytes}
	_, s.streamErr = io.Copy(w, limited)
	if s.streamErr == nil && limited.N == 0 {
		var probe [1]byte
		if n, _ := s.conn.Read(probe[:]); n > 0 {
			s.streamErr = ErrStreamTooLarge
		}
	}
	_ = s.conn.Close()
	close(s.done)
}
