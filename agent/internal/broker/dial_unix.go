//go:build unix

package broker

import (
	"context"
	"net"
)

// dial connects to the broker's Unix socket.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.DialTimeout}
	return d.DialContext(ctx, "unix", c.SocketPath)
}

// halfClose signals the one-request-per-connection protocol (server sees
// EOF immediately).
func halfClose(conn net.Conn) {
	if tc, ok := conn.(*net.UnixConn); ok {
		_ = tc.CloseWrite()
	}
}

// probeOperation returns the platform list op used by Ping.
func probeOperation() string { return OpServiceList }

// Unix socket permissions (unix build).
const (
	SocketDirPerms  = 0o750
	SocketFilePerms = 0o660
)

// Listen returns a net.Listener for the platform endpoint (symmetry helper
// used by the daemon main; chmod/chown handled by the daemon).
func Listen(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return ln, nil
}
