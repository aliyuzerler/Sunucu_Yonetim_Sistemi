//go:build unix

package broker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
)

// fakeWebRunner is a hermetic WebRunner recording calls; it lets broker tests
// cover the web family end-to-end without touching a real host.
type fakeWebRunner struct {
	lists  int
	status string
	result webops.SiteResult
	err    error
}

// mustValidate runs broker validation and fails the test on rejection.
func mustValidate(t *testing.T, req *Request) validatedCall {
	t.Helper()
	call, err := validateRequest(req)
	if err != nil {
		t.Fatalf("istek doğrulanmalı: %v", err)
	}
	return call
}

func (f *fakeWebRunner) List(context.Context, int) ([]webops.SiteInfo, error) {
	f.lists++
	if f.err != nil {
		return nil, f.err
	}
	return []webops.SiteInfo{{Name: "blog", Backend: webops.BackendStatic, Port: 8080, Enabled: true}}, nil
}

func (f *fakeWebRunner) Status(_ context.Context, site string) (webops.SiteStatus, error) {
	return webops.SiteStatus{Site: webops.SiteInfo{Name: site, Enabled: true}}, f.err
}

func (f *fakeWebRunner) Create(_ context.Context, spec webops.SiteSpec) (webops.SiteResult, error) {
	return webops.SiteResult{Site: spec.Name, Action: "create", Executed: true}, f.err
}

func (f *fakeWebRunner) Update(_ context.Context, spec webops.SiteSpec) (webops.SiteResult, error) {
	return webops.SiteResult{Site: spec.Name, Action: "update", Executed: true}, f.err
}

func (f *fakeWebRunner) Disable(_ context.Context, site string) (webops.SiteResult, error) {
	return webops.SiteResult{Site: site, Action: "disable", Executed: true}, f.err
}

func (f *fakeWebRunner) Delete(_ context.Context, site string) (webops.SiteResult, error) {
	return webops.SiteResult{Site: site, Action: "delete", Executed: true}, f.err
}

func (f *fakeWebRunner) PHPVersions(context.Context) (webops.PHPMatrix, error) {
	return webops.PHPMatrix{Supported: webops.PHPSupported}, f.err
}

// TestWebFamilyDisabledByDefault: without Config.Web the family is rejected
// as unsupported — typed, audited, never guessed (ADR-0012 opt-in).
func TestWebFamilyDisabledByDefault(t *testing.T) {
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
	req := &Request{V: ProtocolV1, Operation: OpWebSiteList, RequestID: "t1", Params: []byte("{}")}
	res := srv.execute(req, mustValidate(t, req))
	if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
		t.Fatalf("web ailesi kapalıyken unsupported beklenir: %+v", res)
	}
	req2 := &Request{V: ProtocolV1, Operation: OpWebSiteCreate, RequestID: "t2",
		Params: []byte(`{"name":"blog","server_names":["b.com"],"backend":"static"}`)}
	res2 := srv.execute(req2, mustValidate(t, req2))
	if res2.Status != StatusRejected {
		t.Fatalf("create da unsupported olmalı: %+v", res2)
	}
}

// TestWebFamilyValidation: broker strict validation negatives (mirrors the
// agent-side schema; payload never reaches the runner).
func TestWebFamilyValidation(t *testing.T) {
	negatives := []struct {
		op  string
		req string
	}{
		{OpWebSiteList, `{"limit":99999}`},
		{OpWebSiteStatus, `{"site":"-bad"}`},
		{OpWebSiteStatus, `{"site":"Blog"}`},
		{OpWebSiteDisable, `{"site":"a b"}`},
		{OpWebSiteDelete, `{"site":"../etc"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":[],"backend":"static"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"cgi"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"php"}`}, // php sürümü yok
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"php","php_version":"7.4"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"proxy","proxy_target":"10.0.0.1:80"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"static","unknown_field":1}`},
		{OpWebSiteUpdate, `{"name":"x y","server_names":["b.com"],"backend":"static"}`},
		{OpWebPHPList, `{"x":1}`},
	}
	for i, n := range negatives {
		req := &Request{V: ProtocolV1, Operation: n.op, RequestID: "neg", Params: []byte(n.req)}
		if _, err := validateRequest(req); err == nil {
			t.Errorf("neg[%d] %s %s reddedilmeli", i, n.op, n.req)
		}
	}
	// Pozitifler.
	pos := []struct {
		op  string
		req string
	}{
		{OpWebSiteList, `{}`},
		{OpWebSiteList, `{"limit":50}`},
		{OpWebSiteStatus, `{"site":"blog"}`},
		{OpWebSiteCreate, `{"name":"blog","server_names":["b.com"],"backend":"static"}`},
		{OpWebSiteCreate, `{"name":"shop","server_names":["s.com"],"backend":"php","php_version":"8.3","max_children":25}`},
		{OpWebSiteCreate, `{"name":"app","server_names":["a.com"],"backend":"proxy","proxy_target":"unix:/run/app.sock"}`},
		{OpWebSiteUpdate, `{"name":"blog","server_names":["b.com"],"backend":"static","listen_port":8080}`},
		{OpWebSiteDelete, `{"site":"blog"}`},
		{OpWebPHPList, `{}`},
	}
	for i, p := range pos {
		req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
		if _, err := validateRequest(req); err != nil {
			t.Errorf("pos[%d] %s %s reddedildi: %v", i, p.op, p.req, err)
		}
	}
}

// TestWebFamilyExecuteHappy: with Config.Web wired the family executes and
// returns typed results.
func TestWebFamilyExecuteHappy(t *testing.T) {
	fw := &fakeWebRunner{}
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}, Web: fw})
	req := &Request{V: ProtocolV1, Operation: OpWebSiteList, RequestID: "t3", Params: []byte(`{"limit":10}`)}
	res := srv.execute(req, mustValidate(t, req))
	if res.Status != StatusSucceeded || fw.lists != 1 {
		t.Fatalf("list yürütülmeli: %+v", res)
	}
	req2 := &Request{V: ProtocolV1, Operation: OpWebSiteCreate, RequestID: "t4",
		Params: []byte(`{"name":"blog","server_names":["b.com"],"backend":"static"}`)}
	res2 := srv.execute(req2, mustValidate(t, req2))
	if res2.Status != StatusSucceeded {
		t.Fatalf("create yürütülmeli: %+v", res2)
	}
}

// TestWebFamilyExecuteErrors: stable error mapping from webops typed errors.
func TestWebFamilyExecuteErrors(t *testing.T) {
	cases := []struct {
		err  error
		cat  string
		stat string
	}{
		{webops.ErrSyntaxCheck, CatExecutorFailed, StatusFailed},
		{webops.ErrPortConflict, CatExecutorFailed, StatusFailed},
		{webops.ErrForeignConfig, CatExecutorFailed, StatusFailed},
		{webops.ErrNotFound, CatInvalidParams, StatusRejected},
		{webops.ErrPHPNotInstalled, CatUnsupportedOp, StatusRejected},
		{webops.ErrUnavailable, CatUnsupportedOp, StatusRejected},
	}
	for i, tc := range cases {
		fw := &fakeWebRunner{err: tc.err}
		srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}, Web: fw})
		req := &Request{V: ProtocolV1, Operation: OpWebSiteList, RequestID: "e", Params: []byte("{}")}
		res := srv.execute(req, mustValidate(t, req))
		if res.ErrorCategory != tc.cat || res.Status != tc.stat {
			t.Errorf("err[%d] = %s/%s, %s/%s beklenir (%v)", i, res.Status, res.ErrorCategory, tc.stat, tc.cat, tc.err)
		}
		if !errors.Is(tc.err, tc.err) {
			t.Fail()
		}
	}
}
