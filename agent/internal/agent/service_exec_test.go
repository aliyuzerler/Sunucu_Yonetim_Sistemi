//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

// startTestBroker serves a broker.Server on a temp socket for executor tests.
func startTestBroker(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "privd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := broker.NewServer(broker.Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		Systemd: &svcopsStub{list: []svcops.UnitInfo{
			{ID: "nginx.service", ActiveState: "active", SubState: "running"},
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	return sock
}

// svcopsStub mirrors the broker test fake without an import cycle.
type svcopsStub struct {
	list []svcops.UnitInfo
}

func (f *svcopsStub) List(_ context.Context) ([]svcops.UnitInfo, error) { return f.list, nil }
func (f *svcopsStub) Status(_ context.Context, unit string) (svcops.UnitInfo, error) {
	return svcops.UnitInfo{ID: unit, ActiveState: "active"}, nil
}
func (f *svcopsStub) Start(_ context.Context, unit string) error   { return nil }
func (f *svcopsStub) Stop(_ context.Context, unit string) error    { return nil }
func (f *svcopsStub) Restart(_ context.Context, unit string) error { return nil }

func TestServiceExecutorViaBroker(t *testing.T) {
	sock := startTestBroker(t)
	ctx := context.Background()

	// status: broker üzerinden başarılı sonuç.
	ex := NewServiceExecutor(broker.OpServiceStatus, sock)
	res, _, err := ex.Execute(ctx, []byte(`{"unit":"nginx.service"}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var out struct {
		Unit svcops.UnitInfo `json:"unit"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Unit.ID != "nginx.service" {
		t.Fatalf("status sonucu: %v %s", err, res)
	}

	// start: broker çağrısı.
	ex = NewServiceExecutor(broker.OpServiceStart, sock)
	if _, _, err := ex.Execute(ctx, []byte(`{"unit":"nginx.service"}`)); err != nil {
		t.Fatalf("start: %v", err)
	}

	// list: limit şeması.
	ex = NewServiceExecutor(broker.OpServiceList, sock)
	res, _, err = ex.Execute(ctx, []byte(`{"limit":1}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var lst struct {
		Units []svcops.UnitInfo `json:"units"`
	}
	if err := json.Unmarshal(res, &lst); err != nil || len(lst.Units) != 1 {
		t.Fatalf("list sonucu: %v %s", err, res)
	}

	// Geçersiz unit AGENT tarafında reddedilir (broker'a gitmez).
	ex = NewServiceExecutor(broker.OpServiceStart, sock)
	if _, _, err := ex.Execute(ctx, []byte(`{"unit":"../etc.service"}`)); err == nil {
		t.Fatalf("geçersiz unit reddedilmeli")
	}
	if _, _, err := ex.Execute(ctx, []byte(`{"unit":"-x.service"}`)); err == nil {
		t.Fatalf("flag-benzeri unit reddedilmeli")
	}
	if _, _, err := ex.Execute(ctx, []byte(`not-json`)); err == nil {
		t.Fatalf("bozuk payload reddedilmeli")
	}
}

// Capability availability: with a live broker the service capabilities are
// available; with a dead socket they are NOT (with a stable reason).
func TestCapabilityProbing(t *testing.T) {
	sock := startTestBroker(t)
	reg, err := DefaultRegistry(sock, agentDefaultDockerForTest)
	if err != nil {
		t.Fatal(err)
	}
	env := capabilityEnvelope(context.Background(), "node-1", reg)
	caps := env.GetCapabilityReport().GetCapabilities()
	// Faz 06+07+08+09+10+11: echo + 5 systemd + pkg.list + 3 firewall + 2
	// compose = 11 available; 12 docker op'unun probe'u /var/run/docker.sock
	// varlığına, 7 web + 3 web.cert + 11 db + 5 fs + 16 mail op'unun probe'u
	// broker ping'ine bakar (toplam 83).
	if len(caps) != 83 {
		t.Fatalf("83 capability: %d", len(caps))
	}
	avail := map[string]bool{}
	for _, c := range caps {
		avail[c.GetCapabilityId()] = c.GetAvailable()
	}
	for _, name := range []string{"agent.echo", "linux.systemd.service.list", "linux.systemd.service.status",
		"linux.systemd.service.start", "linux.systemd.service.stop", "linux.systemd.service.restart",
		"linux.pkg.list", "linux.firewall.status", "linux.firewall.rules.list", "linux.firewall.rule.preview",
		"docker.compose.validate", "docker.compose.deploy"} {
		if !avail[name] {
			t.Fatalf("canlı broker ile %s available olmalı", name)
		}
	}

	dead, err := DefaultRegistry(filepath.Join(t.TempDir(), "yok.sock"), agentDefaultDockerForTest)
	if err != nil {
		t.Fatal(err)
	}
	env = capabilityEnvelope(context.Background(), "node-1", dead)
	for _, c := range env.GetCapabilityReport().GetCapabilities() {
		if c.GetCapabilityId() == "agent.echo" || strings.HasPrefix(c.GetCapabilityId(), "docker.compose.") {
			// echo yerleşik kanıt; compose saf analiz — ikisi de her zaman available.
			if !c.GetAvailable() {
				t.Fatalf("%s her zaman available olmalı", c.GetCapabilityId())
			}
			continue
		}
		if c.GetAvailable() {
			t.Fatalf("ölü broker ile unavailable olmalı: %+v", c)
		}
		// Docker ailesi önce socket varlığına bakar; socket yoksa dürüst
		// socket gerekçesi, varsa broker ping'ine düşer → broker_unreachable
		// (CI-18: docker host'larında soket VAR, gerekçe ortam-bağımlıdır —
		// her ikisi de dürüsttür ve unavailable sonuç değişmez).
		if strings.HasPrefix(c.GetCapabilityId(), "docker.container.") ||
			strings.HasPrefix(c.GetCapabilityId(), "docker.image.") ||
			strings.HasPrefix(c.GetCapabilityId(), "docker.network.") ||
			strings.HasPrefix(c.GetCapabilityId(), "docker.volume.") {
			if !strings.Contains(c.GetReasonUnavailable(), "docker socket") &&
				c.GetReasonUnavailable() != "broker_unreachable" {
				t.Fatalf("docker ailesi socket/broker gerekçesi beklenir: %+v", c)
			}
			continue
		}
		if c.GetReasonUnavailable() != "broker_unreachable" {
			t.Fatalf("broker_unreachable beklenir: %+v", c)
		}
	}
}

// factsEnvelope + metricsEnvelope carry source and timestamp.
func TestFactsAndMetricsEnvelopes(t *testing.T) {
	env := factsEnvelope("node-1", "../osdetect/testdata/ubuntu-24.04")
	if env == nil {
		t.Fatal("facts zarfı nil olmamalı")
	}
	f := env.GetSystemFactsReport()
	if f.GetOsId() != "ubuntu" || f.GetOsVersionId() != "24.04" || f.GetPackageManager() != "apt" {
		t.Fatalf("facts: %+v", f)
	}
	if f.GetCollectedAt() == nil || f.GetSource() == "" {
		t.Fatalf("facts timestamp/kaynak zorunlu: %+v", f)
	}
	if env.GetNodeId() != "node-1" || env.GetRequestId() == "" {
		t.Fatalf("zarf kimliği: %+v", env)
	}

	env = metricsEnvelope("node-1", "../metrics/testdata/debian-13")
	if env == nil {
		t.Fatal("metrics zarfı nil olmamalı")
	}
	m := env.GetMetricsReport()
	if m.GetMemory().GetTotalBytes() != 8154300<<10 {
		t.Fatalf("mem: %+v", m.GetMemory())
	}
	if len(m.GetDisks()) != 2 || m.GetDisks()[0].GetDevice() != "/dev/vda1" {
		t.Fatalf("disks: %+v", m.GetDisks())
	}
	if m.GetCollectedAt() == nil || m.GetSource() == "" {
		t.Fatalf("metrics timestamp/kaynak zorunlu: %+v", m)
	}
	if m.GetUptimeSeconds() <= 0 {
		t.Fatalf("uptime: %v", m.GetUptimeSeconds())
	}
}

func TestClampInterval(t *testing.T) {
	t.Helper()
	if clampInterval(time.Second) != 10*time.Second {
		t.Fatal("alt sınır 10s")
	}
	if clampInterval(2*time.Hour) != 10*time.Minute {
		t.Fatal("üst sınır 10dk")
	}
	if clampInterval(time.Minute) != time.Minute {
		t.Fatal("aralık içi korunmalı")
	}
}
