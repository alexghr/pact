// Package unixhttp provides Unix-socket dialing for HTTP transports.
package unixhttp

import (
	"context"
	"net"
)

// Dialer returns a net/http-compatible dial function for a fixed socket.
// Each connection uses the current socket, allowing forwarded sockets to be replaced.
func Dialer(socket string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
}
