//go:build !windows

package nettrace

import (
	"context"
	"net"
)

func openEndpoint(ctx context.Context, path string) (*net.UnixConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
}
