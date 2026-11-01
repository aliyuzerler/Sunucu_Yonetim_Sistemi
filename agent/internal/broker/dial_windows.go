//go:build windows

package broker

import (
	"context"
	"net"
)

// dial connects to the broker's named pipe.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	return dialPipe(ctx, c.SocketPath)
}

// halfClose is a no-op on pipes (the one-request/one-response protocol is
// driven by the broker closing after the response).
func halfClose(conn net.Conn) {}
