//go:build windows

package broker

import (
	"context"
	"encoding/json"
	"os/user"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// fakeScm records verbs without touching the SCM (hermetic).
type fakeScm struct{ called string }

func (f *fakeScm) List(ctx context.Context) ([]winops.ServiceInfo, error) {
	f.called = "list"
	return []winops.ServiceInfo{{Name: "Dhcp", Status: "running"}}, nil
}
func (f *fakeScm) Status(ctx context.Context, name string) (winops.ServiceInfo, error) {
	f.called = "status"
	return winops.ServiceInfo{Name: name, Status: "running"}, nil
}
func (f *fakeScm) Start(ctx context.Context, name string) error { f.called = "start"; return nil }
func (f *fakeScm) Stop(ctx context.Context, name string) error  { f.called = "stop"; return nil }
func (f *fakeScm) Restart(ctx context.Context, name string) error {
	f.called = "restart"
	return nil
}

// currentUserSID returns the caller's canonical SID (tests run unprivileged
// on CI windows runners).
func currentUserSID(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	return u.Uid // windows: SID
}

// startTestBroker spins a pipe broker and returns the endpoint + cleanup.
func startTestBroker(t *testing.T, sids []string, scm ScmRunner) (string, func()) {
	t.Helper()
	path := `\\.\pipe\server-control-test-` + randSuffix()
	ln, err := ListenPipe(path, "")
	if err != nil {
		t.Fatalf("ListenPipe: %v", err)
	}
	srv := NewServer(Config{
		AllowedSIDs: sids,
		Scm:         scm,
		ExecTimeout: 5 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	return path, func() {
		cancel()
		_ = <-done
	}
}

// TestPipeBrokerAllowAndDeny verifies the kernel-verified caller-SID
// allowlist over a REAL named pipe (CI windows runner, unprivileged user).
func TestPipeBrokerAllowAndDeny(t *testing.T) {
	sid := currentUserSID(t)

	scm := &fakeScm{}
	path, stop := startTestBroker(t, []string{sid}, scm)
	defer stop()

	// ALLOW: kendi SID'imizle list çağrısı çalışmalı.
	cli := NewClient(path)
	res, err := cli.Call(context.Background(), &Request{
		Operation: OpWinServiceList,
		RequestID: "t1",
		Params:    []byte(`{"limit":10}`),
	})
	if err != nil {
		t.Fatalf("allow path: %v", err)
	}
	if res.Status != StatusSucceeded {
		t.Fatalf("allow path durumu: %s (%s)", res.Status, res.ErrorCategory)
	}
	if scm.called != "list" {
		t.Fatalf("scm çağrılmadı: %q", scm.called)
	}

	// DENY: allowlist dışı SID → peer_denied (audit edildiği ayrıca
	// JSONLSink testlerinde doğrulanır).
	other, stop2 := startTestBroker(t, []string{"S-1-1-0"}, &fakeScm{})
	defer stop2()
	res2, err := NewClient(other).Call(context.Background(), &Request{
		Operation: OpWinServiceList, RequestID: "t2", Params: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("deny path transport hatası (yanıt beklenirdi): %v", err)
	}
	if res2.Status != StatusRejected || res2.ErrorCategory != CatPeerDenied {
		t.Fatalf("deny path: %s/%s", res2.Status, res2.ErrorCategory)
	}
}

// TestPipeBrokerRejectsForeignTransport pins IN-01: non-pipe transports are
// rejected by identifyPeer (no identity fallback).
func TestPipeBrokerRejectsForeignTransport(t *testing.T) {
	sid := currentUserSID(t)
	path, stop := startTestBroker(t, []string{sid}, &fakeScm{})
	defer stop()

	cli := NewClient(path)
	// Yanlış şema: strict decode reddi (transport doğrudan pipe olsa da
	// payload katmanı ayrıca doğrulanır).
	res, err := cli.Call(context.Background(), &Request{
		Operation: OpWinServiceRestart, RequestID: "t3",
		Params: json.RawMessage(`{"service":"-bad name;"}`),
	})
	if err != nil {
		t.Fatalf("çağrı: %v", err)
	}
	if res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
		t.Fatalf("geçersiz servis adı reddedilmeli: %s/%s", res.Status, res.ErrorCategory)
	}
}

// randSuffix builds a unique pipe suffix per test run.
func randSuffix() string {
	var b [4]byte
	_, _ = cryptoRandRead(b[:])
	return hexEncode(b[:])
}
