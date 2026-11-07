//go:build !windows

package nettrace

import (
	"context"
	"net"
	"os"
	"strconv"
	"syscall"
)

// endpointTrusted reports whether path is a socket, not a link to one, owned by the user who owns process pid. A
// shared temp directory lets any user create a socket named like the runtime's, and the connection carries a
// tracing request out and an unbounded stream back.
func endpointTrusted(path string, pid int) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Type() != os.ModeSocket {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	owner := os.Getuid()
	if proc, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
		if procStat, ok := proc.Sys().(*syscall.Stat_t); ok {
			owner = int(procStat.Uid)
		}
	}
	return int(stat.Uid) == owner
}

func openEndpoint(ctx context.Context, path string) (*net.UnixConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
}
