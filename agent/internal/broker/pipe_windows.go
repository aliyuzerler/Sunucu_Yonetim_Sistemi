//go:build windows

package broker

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pipe endpoint constants (ADR-0009).
const (
	// DefaultPipePath is the default named-pipe endpoint.
	DefaultPipePath = `\\.\pipe\server-control-privd`
	// DefaultSDDL grants full access to SYSTEM and Administrators only;
	// operators APPEND the agent account's SID:
	//   D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;S-1-5-21-…)
	DefaultSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	// SocketDirPerms / SocketFilePerms have no ACL meaning on windows; kept
	// for call-site symmetry with the unix build.
	SocketDirPerms  = 0o750
	SocketFilePerms = 0o660
)

var errPipeClosed = errors.New("pipe dinleyici kapalı")

// pipeAddr is the net.Addr of a named pipe endpoint.
type pipeAddr struct{ path string }

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return a.path }

// pipeListener is a blocking-mode named pipe net.Listener. One pipe instance
// is always pre-created, so concurrent dials queue on the kernel pipe name
// and no connection is lost. No overlapped IO is used (synchronous, simple,
// auditable); Close breaks a pending ConnectNamedPipe via handle close.
type pipeListener struct {
	path string
	sddl string

	mu     sync.Mutex
	next   windows.Handle // pre-created, not yet connected
	closed bool
}

// ListenPipe creates the listener and its first pipe instance.
func ListenPipe(path, sddl string) (*pipeListener, error) {
	if path == "" {
		path = DefaultPipePath
	}
	if sddl == "" {
		sddl = DefaultSDDL
	}
	l := &pipeListener{path: path, sddl: sddl}
	h, err := l.createInstance(true)
	if err != nil {
		return nil, err
	}
	l.next = h
	return l, nil
}

// createInstance creates one pipe instance with the configured SDDL.
func (l *pipeListener) createInstance(first bool) (windows.Handle, error) {
	mode := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		mode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	pipeMode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE |
		windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	var sa *windows.SecurityAttributes
	if l.sddl != "" {
		sd, err := windows.SecurityDescriptorFromString(l.sddl)
		if err != nil {
			return 0, err
		}
		sa = &windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
		}
	}
	return windows.CreateNamedPipe(windows.StringToUTF16Ptr(l.path), mode, pipeMode,
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, sa)
}

// Accept blocks until one client connects.
func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errPipeClosed
	}
	h := l.next
	l.next = 0
	l.mu.Unlock()
	if h == 0 {
		return nil, errPipeClosed
	}

	err := windows.ConnectNamedPipe(h, nil)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) && !errors.Is(err, windows.ERROR_NO_DATA) {
		_ = windows.CloseHandle(h)
		// Listener kapatıldıysa sessiz çık; değilse hatayı bildir.
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, errPipeClosed
		}
		return nil, err
	}

	// Kapanış sırasında mı uyandı? (Close'un self-connect uyanışı dahil.)
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		_ = windows.CloseHandle(h)
		return nil, errPipeClosed
	}

	// Yeni bekleyen örneği hemen yarat (kabul döngüsü kayıpsız olsun).
	nh, nerr := l.createInstance(false)
	l.mu.Lock()
	if nerr == nil {
		l.next = nh
	} else if !l.closed {
		// Örnek yaratılamadı: dinleyiciyi yeniden denenebilir bırak.
		l.next = 0
	}
	l.mu.Unlock()

	return &pipeConn{handle: h, file: newPipeFile(h, l.path), local: pipeAddr{l.path}}, nil
}

// Close stops the listener. On Windows a synchronous ConnectNamedPipe is NOT
// reliably unblocked by CloseHandle from another thread (CI bulgusu CI-10),
// so Close wakes the pending Accept by connecting to our own pipe once.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()

	// Self-connect wakeup: Accept içindeki ConnectNamedPipe döner; Accept
	// closed bayrağını görür ve errPipeClosed ile çıkar (Serve sonlanır).
	wakeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		conn, err := dialPipe(wakeCtx, l.path)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-wakeCtx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{l.path} }

// Listen returns a net.Listener for the platform endpoint (symmetry helper
// used by the daemon main).
func Listen(path string) (net.Listener, error) {
	return ListenPipe(path, DefaultSDDL)
}

// probeOperation returns the platform list op used by Ping.
func probeOperation() string { return OpWinServiceList }
