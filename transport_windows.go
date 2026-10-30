//go:build windows

package nettrace

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// pipeBusyRetry is how long to wait before retrying a pipe whose every instance is in use.
const pipeBusyRetry = 50 * time.Millisecond

// errPipeBusy is ERROR_PIPE_BUSY and errPipeNotConnected is ERROR_PIPE_NOT_CONNECTED, which package syscall does
// not name.
const (
	errPipeBusy         = syscall.Errno(231)
	errPipeNotConnected = syscall.Errno(233)
)

// pipe ends the stream at ERROR_PIPE_NOT_CONNECTED. The runtime ends a session by flushing and then disconnecting
// its end of the pipe, after which a read fails with that error rather than returning EOF; NamedPipeClientStream in
// .NET reads it as EOF too. os already maps ERROR_BROKEN_PIPE to EOF.
type pipe struct{ *os.File }

func (p pipe) Read(b []byte) (int, error) {
	n, err := p.File.Read(b)
	if errors.Is(err, errPipeNotConnected) {
		return n, io.EOF
	}
	return n, err
}

// openEndpoint opens the runtime's diagnostics named pipe, which Windows exposes as a file path. A busy pipe is
// retried until ctx ends, as the runtime's own client does.
func openEndpoint(ctx context.Context, path string) (io.ReadWriteCloser, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // G304: path is the pipe name this package built from a pid
		if err == nil {
			return pipe{file}, nil
		}
		if !errors.Is(err, errPipeBusy) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pipeBusyRetry):
		}
	}
}
