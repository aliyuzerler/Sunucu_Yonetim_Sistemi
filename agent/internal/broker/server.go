// Package broker implements the Faz 04/05 local privileged broker protocol.
//
// Trust model (docs/architecture/security-boundaries.md, ADR-0009):
//   - The broker (server-privd) is a SEPARATE binary and identity running
//     with elevated privileges (root on Linux, LocalSystem on Windows); the
//     agent runs unprivileged.
//   - Linux: the broker listens ONLY on a mode-0660 Unix socket; peer
//     identity is verified with SO_PEERCRED against an explicit UID
//     allowlist. Windows: the broker listens ONLY on a named pipe with a
//     restrictive SDDL; caller identity is kernel-verified via
//     impersonation against an explicit SID allowlist. Default deny.
//   - Requests are typed JSON with allowlisted operations; parameters are
//     strictly validated. Inputs are NEVER merged into shell strings.
//   - Every accepted request (and every rejection) is audited.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"
)

// AuditEntry is one audited broker action (JSONL). Fields echo caller
// identity, target, sanitized params and outcome (docs/architecture/
// agent-architecture.md: broker audit records).
type AuditEntry struct {
	Time          string `json:"time"`
	ActorUID      uint32 `json:"actor_uid,omitempty"`
	ActorGID      uint32 `json:"actor_gid,omitempty"`
	ActorSID      string `json:"actor_sid,omitempty"` // windows caller SID
	PID           int32  `json:"pid"`
	Operation     string `json:"operation"`
	TaskID        string `json:"task_id,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Target        string `json:"target,omitempty"` // validated before echo
	Status        string `json:"status"`
	ErrorCategory string `json:"error_category,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

// AuditSink records audit entries. JSONLSink is the file implementation.
type AuditSink interface {
	Record(AuditEntry)
}

// JSONLSink appends one JSON object per line to a file opened 0600.
type JSONLSink struct {
	path string
	mu   sync.Mutex
}

// NewJSONLSink opens (or creates) the audit file with 0600 perms.
func NewJSONLSink(path string) (*JSONLSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path is an operator-provided daemon flag
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &JSONLSink{path: path}, nil
}

// Record appends one entry; errors are swallowed (audit best-effort at the
// broker; the control-plane audit remains the authoritative trail).
func (s *JSONLSink) Record(e AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // operator-provided path
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// Server is the privileged broker daemon core (transport- and OS-neutral:
// listener wiring and peer identity live in auth_*/listen_*/exec_* files).
type Server struct {
	cfg    Config
	sem    chan struct{}
	logger *slog.Logger
}

// NewServer validates config and builds the daemon core.
func NewServer(cfg Config) *Server {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 4
	}
	if cfg.ExecTimeout <= 0 {
		cfg.ExecTimeout = 60 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent), logger: cfg.Logger}
}

// Handle processes ONE connection: identify the peer (kernel-verified),
// read one bounded request, validate, execute with timeout/concurrency
// bounds, audit and answer. The identity extractor is per-OS:
//   - unix: *net.UnixConn → SO_PEERCRED (auth_unix.go)
//   - windows: *pipeConn → impersonation-based caller SID (auth_windows.go)
//   - ANY other transport is REJECTED (defense in depth: the test fallback
//     of Faz 04 was removed — IN-01).
func (s *Server) Handle(conn net.Conn) {
	ident, err := identifyPeer(conn)
	if err != nil {
		s.logger.Warn("broker_peer_identity_failed", slog.String("error", err.Error()))
		_ = writeResponse(conn, &Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatPeerDenied})
		_ = conn.Close()
		return
	}
	s.handleCreds(conn, ident)
}

func (s *Server) handleCreds(conn net.Conn, ident PeerIdentity) {
	start := time.Now()
	base := AuditEntry{
		Time: start.UTC().Format(time.RFC3339Nano),
		PID:  ident.PID,
	}
	if ident.Unix {
		base.ActorUID, base.ActorGID = ident.UID, ident.GID
	} else {
		base.ActorSID = ident.SID
	}

	respond := func(res *Response, entry AuditEntry) {
		entry.Status = res.Status
		entry.ErrorCategory = res.ErrorCategory
		entry.DurationMS = time.Since(start).Milliseconds()
		// Audit ÖNCE yazılır: istemci yanıtı aldıktan sonra test/izleyici
		// audit kaydını hemen görebilir (yarışsız gözlem).
		if s.cfg.Audit != nil {
			s.cfg.Audit.Record(entry)
		}
		_ = writeResponse(conn, res)
		_ = conn.Close()
	}

	if !s.peerAllowed(ident) {
		respond(&Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatPeerDenied},
			base)
		return
	}

	line, err := readBoundedLine(conn, maxRequestBytes)
	if err != nil {
		respond(&Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatInvalidRequest},
			base)
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		respond(&Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatInvalidRequest},
			base)
		return
	}
	entry := base
	entry.Operation = req.Operation
	entry.TaskID = sanitizeID(req.TaskID)
	entry.RequestID = sanitizeID(req.RequestID)
	entry.CorrelationID = sanitizeID(req.CorrID)

	call, verr := validateRequest(&req)
	if verr != nil {
		res := &Response{V: ProtocolV1, Operation: req.Operation, RequestID: sanitizeID(req.RequestID),
			Status: StatusRejected, ErrorCategory: Category(verr), Output: verr.Error()}
		respond(res, entry)
		return
	}
	entry.Target = call.target
	res := s.execute(&req, call)
	res.V = ProtocolV1
	res.Operation = req.Operation
	res.RequestID = sanitizeID(req.RequestID)
	res.TaskID = entry.TaskID
	respond(res, entry)
}

func writeResponse(conn net.Conn, res *Response) error {
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}

// readBoundedLine reads at most limit bytes and errors on overflow.
func readBoundedLine(conn net.Conn, limit int64) ([]byte, error) {
	buf := make([]byte, limit)
	var total int
	for total < int(limit) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			if total > 0 {
				break
			}
			return nil, err
		}
		if idx := indexByte(buf[:total], '\n'); idx >= 0 {
			out := buf[:idx]
			if len(out) == 0 {
				return nil, errors.New("boş satır")
			}
			return out, nil
		}
		if total >= int(limit) {
			return nil, errors.New("istek çok büyük")
		}
	}
	out := buf[:total]
	if len(out) == 0 {
		return nil, errors.New("boş istek")
	}
	return out, nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func clampStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Serve accepts connections until the listener closes or ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return fmt.Errorf("accept: %w", err)
		}
		go s.Handle(conn)
	}
}
