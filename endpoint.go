package nettrace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// ErrNoEndpoint means no diagnostics endpoint exists for the process: it is not a .NET process, has not created
// its endpoint yet, or has diagnostics disabled. It also matches os.ErrNotExist.
var ErrNoEndpoint = errors.New("nettrace: no diagnostics endpoint for the process")

func endpointCandidates(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, errors.New("nettrace: invalid process id")
	}
	if runtime.GOOS == "windows" {
		return []string{fmt.Sprintf(`\\.\pipe\dotnet-diagnostic-%d`, pid)}, nil
	}
	// The runtime creates its socket under its own TMPDIR, which in a container is a path inside the container's
	// root: /proc/<pid>/root reaches it from the host.
	root := filepath.Join("/proc", strconv.Itoa(pid), "root")
	dirs := []string{os.TempDir(), filepath.Join(root, "tmp")}
	if env, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ")); err == nil {
		for entry := range strings.SplitSeq(string(env), "\x00") {
			if value, ok := strings.CutPrefix(entry, "TMPDIR="); ok && value != "" {
				dirs = append(dirs, value, filepath.Join(root, value))
			}
		}
	}
	seen := make(map[string]bool)
	var matches []string
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		found, err := filepath.Glob(filepath.Join(dir, fmt.Sprintf("dotnet-diagnostic-%d-*-socket", pid)))
		if err != nil {
			return nil, err
		}
		matches = append(matches, found...)
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w (pid %d): %w", ErrNoEndpoint, pid, os.ErrNotExist)
	}
	return matches, nil
}

func dialEndpoint(ctx context.Context, pid int) (io.ReadWriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := endpointCandidates(pid)
	if err != nil {
		return nil, err
	}
	var last error
	for _, path := range paths {
		conn, err := openEndpoint(ctx, path)
		if err == nil {
			return conn, nil
		}
		last = err
	}
	// The Windows pipe name is not discovered first, so a pipe that does not exist yet surfaces only here; so does a
	// socket removed between the glob and the dial.
	if errors.Is(last, os.ErrNotExist) {
		return nil, fmt.Errorf("%w (pid %d): %w", ErrNoEndpoint, pid, last)
	}
	return nil, last
}
