//go:build windows

package nettrace

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// pipeBusyRetry is how long to wait before retrying a pipe whose every instance is in use.
const pipeBusyRetry = 50 * time.Millisecond

// errPipeBusy is ERROR_PIPE_BUSY, which package syscall does not name.
const errPipeBusy = syscall.Errno(231)

// openEndpoint opens the runtime's diagnostics named pipe, which Windows exposes as a file path. A busy pipe is
// retried until ctx ends, as the runtime's own client does.
func openEndpoint(ctx context.Context, path string) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // G304: path is the pipe name this package built from a pid
		if !errors.Is(err, errPipeBusy) {
			return file, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pipeBusyRetry):
		}
	}
}
