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
	"syscall"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

// AuditEntry is one audited broker action (JSONL). Fields echo caller
// identity, target, sanitized params and outcome (docs/architecture/
// agent-architecture.md: broker audit records).
type AuditEntry struct {
	Time          string `json:"time"`
	ActorUID      uint32 `json:"actor_uid"`
	ActorGID      uint32 `json:"actor_gid"`
	PID           int32  `json:"pid"`
	Operation     string `json:"operation"`
	TaskID        string `json:"task_id,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Target        string `json:"target,omitempty"` // unit name (validated before echo)
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

// SystemdRunner executes one service operation. *svcops.Manager implements it;
// the interface keeps the broker tests hermetic.
type SystemdRunner interface {
	List(ctx context.Context) ([]svcops.UnitInfo, error)
	Status(ctx context.Context, unit string) (svcops.UnitInfo, error)
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
}

// Config is the broker daemon configuration.
type Config struct {
	AllowedUIDs   []uint32      // explicit allowlist; empty = deny all
	MaxConcurrent int           // parallel executions (default 4)
	ExecTimeout   time.Duration // per-op timeout (default 60s)
	Systemd       SystemdRunner
	Audit         AuditSink
	Logger        *slog.Logger
}

// Server is the privileged broker daemon core (transport-independent core +
// Unix listener wiring in Serve).
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

// AllowedUID reports whether uid may call the broker (default deny).
func (s *Server) AllowedUID(uid uint32) bool {
	for _, u := range s.cfg.AllowedUIDs {
		if u == uid {
			return true
		}
	}
	return false
}

// peerCreds extracts SO_PEERCRED credentials from a Unix connection.
func peerCreds(conn *net.UnixConn) (uid, gid uint32, pid int32, err error) {
	sc, serr := conn.SyscallConn()
	if serr != nil {
		return 0, 0, 0, serr
	}
	var ucred *syscall.Ucred
	var ctlErr error
	if ctlErr = sc.Control(func(fd uintptr) {
		ucred, ctlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); ctlErr != nil {
		return 0, 0, 0, ctlErr
	}
	if ctlErr != nil {
		return 0, 0, 0, ctlErr
	}
	if ucred == nil {
		return 0, 0, 0, errors.New("peer kimliği alınamadı")
	}
	return ucred.Uid, ucred.Gid, ucred.Pid, nil
}

// Handle processes ONE connection: authenticate via SO_PEERCRED, read one
// bounded request, validate, execute with timeout/concurrency bounds, audit
// and answer. Exported for hermetic tests over a real temp socket.
func (s *Server) Handle(conn net.Conn) {
	uc, ok := conn.(*net.UnixConn)
	if ok {
		uid, gid, pid, err := peerCreds(uc)
		if err != nil {
			s.logger.Warn("broker_peer_creds_failed", slog.String("error", err.Error()))
			_ = writeResponse(conn, &Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatPeerDenied})
			return
		}
		s.handleCreds(conn, uid, gid, pid)
		return
	}
	// Test transport without peer creds: identity 0/0/0.
	s.handleCreds(conn, 0, 0, 0)
}

func (s *Server) handleCreds(conn net.Conn, uid, gid uint32, pid int32) {
	start := time.Now()
	base := AuditEntry{Time: start.UTC().Format(time.RFC3339Nano), ActorUID: uid, ActorGID: gid, PID: pid}

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

	if !s.AllowedUID(uid) {
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

	unit, listLimit, verr := validateRequest(&req)
	if verr != nil {
		res := &Response{V: ProtocolV1, Operation: req.Operation, RequestID: sanitizeID(req.RequestID),
			Status: StatusRejected, ErrorCategory: Category(verr), Output: verr.Error()}
		respond(res, entry)
		return
	}
	entry.Target = unit
	res := s.execute(&req, unit, listLimit)
	res.V = ProtocolV1
	res.Operation = req.Operation
	res.RequestID = sanitizeID(req.RequestID)
	res.TaskID = entry.TaskID
	respond(res, entry)
}

// execute runs the validated operation with bounded concurrency + timeout.
func (s *Server) execute(req *Request, unit string, listLimit int) *Response {
	res := &Response{Status: StatusFailed}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-time.After(5 * time.Second):
		res.Status = StatusRejected
		res.ErrorCategory = CatRateLimited
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ExecTimeout)
	defer cancel()
	sysd := s.cfg.Systemd
	if sysd == nil {
		sysd = &svcops.Manager{}
	}

	var result any
	var err error
	switch req.Operation {
	case OpServiceList:
		var units []svcops.UnitInfo
		units, err = sysd.List(ctx)
		if units != nil && len(units) > listLimit {
			units = units[:listLimit]
		}
		result = map[string]any{"units": units}
	case OpServiceStatus:
		var u svcops.UnitInfo
		u, err = sysd.Status(ctx, unit)
		result = map[string]any{"unit": u}
	case OpServiceStart:
		err = sysd.Start(ctx, unit)
		result = map[string]any{"unit": unit, "action": "start"}
	case OpServiceStop:
		err = sysd.Stop(ctx, unit)
		result = map[string]any{"unit": unit, "action": "stop"}
	case OpServiceRestart:
		err = sysd.Restart(ctx, unit)
		result = map[string]any{"unit": unit, "action": "restart"}
	default:
		// validateRequest garantisi dışına çıkılmamalı; savunma amaçlı.
		res.Status = StatusRejected
		res.ErrorCategory = CatUnsupportedOp
		return res
	}

	if err != nil {
		res.Output = clampStr(err.Error(), 500)
		switch {
		case errors.Is(err, svcops.ErrInvalidUnit):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, svcops.ErrTimeout):
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorTimedOut
		default:
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorFailed
		}
		return res
	}
	b, _ := json.Marshal(result)
	res.Status = StatusSucceeded
	res.Result = b
	return res
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
