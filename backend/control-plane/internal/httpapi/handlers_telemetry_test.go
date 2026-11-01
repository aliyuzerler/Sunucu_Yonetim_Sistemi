package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
)

// testCfgForTelemetry builds a minimal valid config for endpoint tests.
func testCfgForTelemetry() config.Config {
	cfg, err := config.FromEnv(map[string]string{})
	if err != nil {
		panic(err)
	}
	return cfg
}

// fakeTelemetryStore is an in-memory Store for endpoint tests.
type fakeTelemetryStore struct {
	facts map[string]*telemetry.SystemFacts
	snaps map[string][]*telemetry.Snapshot
}

func newFakeTelemetryStore() *fakeTelemetryStore {
	return &fakeTelemetryStore{facts: map[string]*telemetry.SystemFacts{}, snaps: map[string][]*telemetry.Snapshot{}}
}

func (f *fakeTelemetryStore) UpsertFacts(_ context.Context, fact *telemetry.SystemFacts) error {
	f.facts[fact.NodeID] = fact
	return nil
}

func (f *fakeTelemetryStore) GetFacts(_ context.Context, nodeID string) (*telemetry.SystemFacts, error) {
	if fact, ok := f.facts[nodeID]; ok {
		return fact, nil
	}
	return nil, telemetry.ErrNotFound
}

func (f *fakeTelemetryStore) InsertMetrics(_ context.Context, nodeID string, s *telemetry.Snapshot) error {
	f.snaps[nodeID] = append(f.snaps[nodeID], s)
	return nil
}

func (f *fakeTelemetryStore) LatestMetrics(_ context.Context, nodeID string) (*telemetry.Snapshot, error) {
	list := f.snaps[nodeID]
	if len(list) == 0 {
		return nil, telemetry.ErrNotFound
	}
	return list[len(list)-1], nil
}

func (f *fakeTelemetryStore) ListMetrics(_ context.Context, nodeID string, limit int) ([]*telemetry.Snapshot, error) {
	list := f.snaps[nodeID]
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (f *fakeTelemetryStore) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

// Telemetry routes exist and require authentication: unauthenticated requests
// get 401 (not 404), proving the routes are wired.
func TestTelemetryRoutesRequireAuth(t *testing.T) {
	h := New(Options{
		Cfg:       testCfgForTelemetry(),
		Logger:    slog.New(slog.DiscardHandler),
		Pinger:    pingerFunc(func(context.Context) error { return nil }),
		Telemetry: telemetry.NewService(newFakeTelemetryStore()),
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	nodeID := "11111111-1111-1111-1111-111111111111"
	for _, path := range []string{
		"/api/v1/nodes/" + nodeID + "/facts/system",
		"/api/v1/nodes/" + nodeID + "/metrics/latest",
		"/api/v1/nodes/" + nodeID + "/metrics",
	} {
		resp := doGet(t, srv, path)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s oturumsuz 401 olmalı: %d", path, resp.StatusCode)
		}
	}
}

// Telemetry nil → 503 not_configured (routes still registered behind auth).
func TestTelemetryNotConfiguredStaysAuthWired(t *testing.T) {
	h := New(Options{Cfg: testCfgForTelemetry(), Logger: slog.New(slog.DiscardHandler), Pinger: pingerFunc(func(context.Context) error { return nil })})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/api/v1/nodes/11111111-1111-1111-1111-111111111111/metrics/latest")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("telemetri yapılandırılmamışken de auth öncelikli: %d", resp.StatusCode)
	}
}

// JSON shape sanity: telemetry payloads serialize the documented fields.
func TestTelemetryJSONShape(t *testing.T) {
	snap := &telemetry.Snapshot{
		CollectedAt: time.Now().UTC(), Source: "procfs",
		CPU:    telemetry.CPUSample{Percent: 1},
		Memory: telemetry.MemorySample{TotalBytes: 1},
		Disks:  []telemetry.DiskSample{{Device: "d"}}, Networks: []telemetry.NetworkSample{},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"collected_at", "source", "cpu", "memory", "disks", "networks", "uptime_seconds"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("JSON alanı eksik: %s (%v)", key, m)
		}
	}
}
