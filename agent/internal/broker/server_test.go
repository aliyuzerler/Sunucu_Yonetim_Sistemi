package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

// fakeSystemd records verbs without touching the host.
type fakeSystemd struct {
	list    []svcops.UnitInfo
	calls   []string
	failOp  string
	failErr error
}

func (f *fakeSystemd) List(_ context.Context) ([]svcops.UnitInfo, error) {
	f.calls = append(f.calls, "list")
	if f.failOp == "list" {
		return nil, f.failErr
	}
	return f.list, nil
}

func (f *fakeSystemd) Status(_ context.Context, unit string) (svcops.UnitInfo, error) {
	f.calls = append(f.calls, "status:"+unit)
	if f.failOp == "status" {
		return svcops.UnitInfo{}, f.failErr
	}
	return svcops.UnitInfo{ID: unit, ActiveState: "active", SubState: "running"}, nil
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	f.calls = append(f.calls, "start:"+unit)
	if f.failOp == "start" {
		return f.failErr
	}
	return nil
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	f.calls = append(f.calls, "stop:"+unit)
	if f.failOp == "stop" {
		return f.failErr
	}
	return nil
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	f.calls = append(f.calls, "restart:"+unit)
	if f.failOp == "restart" {
		return f.failErr
	}
	return nil
}

type memAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (m *memAudit) Record(e AuditEntry) {
	m.mu.Lock()
	m.entries = append(m.entries, e)
	m.mu.Unlock()
}

func (m *memAudit) all() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries
}

// startBroker serves a real Unix socket on a temp dir; the test process's own
// UID is used as the allowed peer (SO_PEERCRED reports it).
func startBroker(t *testing.T, allowSelf bool, sysd SystemdRunner) (*Client, *memAudit) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "privd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	audit := &memAudit{}
	var uids []uint32
	if allowSelf {
		uids = []uint32{uint32(os.Getuid())}
	}
	srv := NewServer(Config{AllowedUIDs: uids, Systemd: sysd, Audit: audit, ExecTimeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	return &Client{SocketPath: sock, DialTimeout: time.Second, CallTimeout: 5 * time.Second}, audit
}

func TestAllowlistAndServiceOps(t *testing.T) {
	sysd := &fakeSystemd{list: []svcops.UnitInfo{
		{ID: "nginx.service", ActiveState: "active", SubState: "running", Description: "A high performance web server"},
		{ID: "cron.service", ActiveState: "active", SubState: "waiting"},
	}}
	cli, audit := startBroker(t, true, sysd)
	ctx := context.Background()

	// list
	res, err := cli.Call(ctx, &Request{V: ProtocolV1, Operation: OpServiceList, RequestID: "r-1"})
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("list: %v %+v", err, res)
	}
	var out struct {
		Units []svcops.UnitInfo `json:"units"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Units) != 2 {
		t.Fatalf("list sonucu: %v %s", err, res.Result)
	}
	if out.Units[0].Description != "A high performance web server" {
		t.Fatalf("description boşluklu korunmalı: %+v", out.Units[0])
	}

	// status
	res, err = cli.Call(ctx, &Request{V: ProtocolV1, Operation: OpServiceStatus, Params: []byte(`{"unit":"nginx.service"}`)})
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("status: %v %+v", err, res)
	}

	// start/stop/restart
	for _, op := range []string{OpServiceStart, OpServiceStop, OpServiceRestart} {
		res, err = cli.Call(ctx, &Request{V: ProtocolV1, Operation: op, Params: []byte(`{"unit":"nginx.service"}`), TaskID: "t-1"})
		if err != nil || res.Status != StatusSucceeded {
			t.Fatalf("%s: %v %+v", op, err, res)
		}
	}

	// Audit zorunluluğu: her kabul edilen istek audit kaydı üretir.
	if got := len(audit.all()); got != 5 {
		t.Fatalf("5 audit kaydı beklenir: %d", got)
	}
	if audit.all()[0].Operation != OpServiceList || audit.all()[0].Status != StatusSucceeded {
		t.Fatalf("ilk audit: %+v", audit.all()[0])
	}

	// Argv çağrıları (shell string değil) doğru birime gitti.
	calls := sysd.calls
	if len(calls) != 5 || calls[0] != "list" || calls[1] != "status:nginx.service" || calls[4] != "restart:nginx.service" {
		t.Fatalf("systemd çağrıları: %v", calls)
	}
}

func TestPeerDeniedWhenUIDNotAllowlisted(t *testing.T) {
	cli, audit := startBroker(t, false, &fakeSystemd{}) // boş allowlist
	res, err := cli.Call(context.Background(), &Request{V: ProtocolV1, Operation: OpServiceList})
	if err != nil {
		t.Fatalf("transport hatası: %v", err)
	}
	if res.Status != StatusRejected || res.ErrorCategory != CatPeerDenied {
		t.Fatalf("reddedilmeli: %+v", res)
	}
	if len(audit.all()) != 1 || audit.all()[0].ErrorCategory != CatPeerDenied {
		t.Fatalf("reddedilen istek auditlenmeli: %+v", audit.all())
	}
}

func TestRejectsUnsupportedOperation(t *testing.T) {
	sysd := &fakeSystemd{}
	cli, audit := startBroker(t, true, sysd)
	res, err := cli.Call(context.Background(), &Request{
		V: ProtocolV1, Operation: "shell.exec", Params: []byte(`{"cmd":"rm -rf /"}`)})
	if err != nil || res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
		t.Fatalf("allowlist dışı op reddedilmeli: %v %+v", err, res)
	}
	// İstek YÜRÜTÜLMEDİ (fake systemd çağrı kaydı yok) + auditlendi.
	if len(sysd.calls) != 0 || len(audit.all()) != 1 {
		t.Fatalf("yürütme veya audit eksik: %v %d", sysd.calls, len(audit.all()))
	}
}

func TestRejectsBadUnitPayloads(t *testing.T) {
	cli, _ := startBroker(t, true, &fakeSystemd{})
	cases := []string{
		`{"unit":"../etc/passwd.service"}`,
		`{"unit":"-bash.service"}`,
		`{"unit":"nginx;reboot.service"}`,
		`{"unit":"nginx .service"}`,
		`{"unit":"nginx$(reboot).service"}`,
		`{"unit":"nginx` + "`id`" + `.service"}`,
		`{"unit":"nginx$IFS.service"}`,
		`{"unit":"%q.service"}`,
		`{"unit":"no-suffix"}`,
		`{"unknown":1}`,
	}
	for _, params := range cases {
		res, err := cli.Call(context.Background(), &Request{V: ProtocolV1, Operation: OpServiceStart, Params: []byte(params)})
		if err != nil {
			t.Fatalf("transport: %v (%s)", err, params)
		}
		if res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
			t.Fatalf("geçersiz unit reddedilmeli %q: %+v", params, res)
		}
	}
}

// A raw socket may deliver non-JSON bytes; the broker must reject without
// executing anything and still audit.
func TestRejectsNonJSONRawRequest(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "privd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	audit := &memAudit{}
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}, Systemd: &fakeSystemd{}, Audit: audit})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, ln) }()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("not-json-at-all\n"))
	buf := make([]byte, 512)
	deadline := time.Now().Add(2 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	n, _ := conn.Read(buf)
	var res Response
	if err := json.Unmarshal(buf[:n], &res); err != nil || res.Status != StatusRejected || res.ErrorCategory != CatInvalidRequest {
		t.Fatalf("bozuk istek reddedilmeli: %v %s", err, buf[:n])
	}
	if len(audit.all()) != 1 {
		t.Fatalf("audit kaydı eksik: %d", len(audit.all()))
	}
}

func TestExecutorFailureMapsToFailed(t *testing.T) {
	cli, _ := startBroker(t, true, &fakeSystemd{
		failOp: "restart", failErr: fmt.Errorf("Job failed. See 'systemctl status nginx.service'"),
	})
	res, err := cli.Call(context.Background(), &Request{V: ProtocolV1, Operation: OpServiceRestart, Params: []byte(`{"unit":"nginx.service"}`)})
	if err != nil || res.Status != StatusFailed || res.ErrorCategory != CatExecutorFailed {
		t.Fatalf("executor hatası failed olmalı: %v %+v", err, res)
	}
}

func TestExecutorTimeoutMapsToTimeoutCategory(t *testing.T) {
	cli, _ := startBroker(t, true, &fakeSystemd{
		failOp: "stop", failErr: fmt.Errorf("%w: stop nginx.service", svcops.ErrTimeout),
	})
	res, err := cli.Call(context.Background(), &Request{V: ProtocolV1, Operation: OpServiceStop, Params: []byte(`{"unit":"nginx.service"}`)})
	if err != nil || res.ErrorCategory != CatExecutorTimedOut {
		t.Fatalf("timeout kategorisi: %v %+v", err, res)
	}
}

func TestValidateRequestDirectly(t *testing.T) {
	if _, _, err := validateRequest(&Request{V: 2, Operation: OpServiceList}); err == nil {
		t.Fatalf("yanlış sürüm reddedilmeli")
	}
	unit, limit, err := validateRequest(&Request{V: ProtocolV1, Operation: OpServiceList})
	if err != nil || limit != 200 || unit != "" {
		t.Fatalf("list varsayılan limit 200: %v %d %v", err, limit, unit)
	}
	if _, _, err = validateRequest(&Request{V: ProtocolV1, Operation: OpServiceList, Params: []byte(`{"limit":501}`)}); err == nil {
		t.Fatalf("limit 501 reddedilmeli")
	}
	if _, _, err = validateRequest(nil); err == nil {
		t.Fatalf("nil istek reddedilmeli")
	}
}

func TestJSONLSinkWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	sink.Record(AuditEntry{Time: time.Now().UTC().Format(time.RFC3339Nano), Operation: OpServiceStart, Status: StatusSucceeded, Target: "nginx.service"})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e AuditEntry
	if err := json.Unmarshal(b, &e); err != nil || e.Operation != OpServiceStart || e.Target != "nginx.service" {
		t.Fatalf("audit satırı: %v %s", err, b)
	}
}

func TestSanitizeID(t *testing.T) {
	if got := sanitizeID("abc-123_0."); got != "abc-123_0." {
		t.Fatalf("geçerli id korunmalı: %q", got)
	}
	if got := sanitizeID("\x01evil id"); got == "\x01evil id" {
		t.Fatalf("geçersiz id sanitize edilmeli: %q", got)
	}
	if got := sanitizeID("a-very-long-id-that-exceeds-the-64-character-boundary-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !strings.HasPrefix(got, "invalid:") {
		t.Fatalf("uzun id reddedilmeli: %q", got)
	}
}

// Ping reflects broker reachability for capability reporting.
func TestPingReachability(t *testing.T) {
	cli, _ := startBroker(t, true, &fakeSystemd{})
	if ok, reason := cli.Ping(context.Background()); !ok || reason != "" {
		t.Fatalf("ping başarılı olmalı: %v %s", ok, reason)
	}
	dead := NewClient(filepath.Join(t.TempDir(), "yok.sock"))
	if ok, reason := dead.Ping(context.Background()); ok || reason != "broker_unreachable" {
		t.Fatalf("ölü socket: %v %s", ok, reason)
	}
}
