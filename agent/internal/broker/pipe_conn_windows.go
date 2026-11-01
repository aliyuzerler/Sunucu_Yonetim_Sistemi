//go:build windows

package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// newPipeFile wraps a connected pipe handle as *os.File (read/write).
func newPipeFile(h windows.Handle, name string) *os.File {
	return os.NewFile(uintptr(h), name)
}

// pipeConn is the server-side connected pipe; it keeps the raw handle so
// the identity layer can impersonate the caller.
type pipeConn struct {
	handle windows.Handle
	file   *os.File
	local  net.Addr
}

func (c *pipeConn) Read(b []byte) (int, error)  { return c.file.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error) { return c.file.Write(b) }

func (c *pipeConn) Close() error {
	// Sunucu tarafı: bağlantıyı kes ve örneği kapat.
	_ = windows.DisconnectNamedPipe(c.handle)
	return c.file.Close()
}

func (c *pipeConn) LocalAddr() net.Addr  { return c.local }
func (c *pipeConn) RemoteAddr() net.Addr { return c.local }

// SetDeadline arms a hard close timer (sync pipes have no kernel deadlines).
func (c *pipeConn) SetDeadline(t time.Time) error { return c.setCloseAt(t) }
func (c *pipeConn) SetReadDeadline(t time.Time) error {
	return c.setCloseAt(t)
}
func (c *pipeConn) SetWriteDeadline(t time.Time) error {
	return c.setCloseAt(t)
}

func (c *pipeConn) setCloseAt(t time.Time) error {
	if t.IsZero() {
		return nil
	}
	d := time.Until(t)
	if d <= 0 {
		_ = c.Close()
		return nil
	}
	time.AfterFunc(d, func() { _ = c.Close() })
	return nil
}

// clientPipeConn is the agent-side connection opened with
// SECURITY_IDENTIFICATION (the server can identify, never impersonate for
// action).
type clientPipeConn struct {
	file *os.File
	addr net.Addr

	mu    sync.Mutex
	timer *time.Timer
}

func (c *clientPipeConn) Read(b []byte) (int, error)  { return c.file.Read(b) }
func (c *clientPipeConn) Write(b []byte) (int, error) { return c.file.Write(b) }
func (c *clientPipeConn) Close() error                { return c.file.Close() }
func (c *clientPipeConn) LocalAddr() net.Addr         { return c.addr }
func (c *clientPipeConn) RemoteAddr() net.Addr        { return c.addr }

func (c *clientPipeConn) SetDeadline(t time.Time) error { return c.setCloseAt(t) }
func (c *clientPipeConn) SetReadDeadline(t time.Time) error {
	return c.setCloseAt(t)
}
func (c *clientPipeConn) SetWriteDeadline(t time.Time) error {
	return c.setCloseAt(t)
}

func (c *clientPipeConn) setCloseAt(t time.Time) error {
	if t.IsZero() {
		return nil
	}
	d := time.Until(t)
	if d <= 0 {
		_ = c.Close()
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(d, func() { _ = c.Close() })
	return nil
}

// dialPipe connects to the broker named pipe with SECURITY_IDENTIFICATION,
// retrying ERROR_PIPE_BUSY until the context deadline.
func dialPipe(ctx context.Context, path string) (net.Conn, error) {
	if path == "" {
		path = DefaultPipePath
	}
	p16 := windows.StringToUTF16Ptr(path)
	for {
		h, err := windows.CreateFile(p16,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, nil, windows.OPEN_EXISTING,
			windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION,
			0)
		if err == nil {
			return &clientPipeConn{
				file: os.NewFile(uintptr(h), path),
				addr: pipeAddr{path},
			}, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("pipe bağlantısı kurulamadı: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
