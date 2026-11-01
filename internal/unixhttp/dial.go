// Package unixhttp provides Unix-socket dialing for HTTP transports.
package unixhttp

import (
	"context"
	"net"
	"path/filepath"
)

// Dialer returns a net/http-compatible dial function for a fixed socket.
// It resolves the path on each connection, then runs check if provided.
func Dialer(socket string, check func(string) error) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		path, err := filepath.EvalSymlinks(socket)
		if err != nil {
			return nil, err
		}
		if check != nil {
			if err := check(path); err != nil {
				return nil, err
			}
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}
}
