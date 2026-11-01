package dockerx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newMockEngine spins an Engine API mock over a throwaway Unix socket and a
// Client pointed at it. Handlers can assert the negotiated version prefix.
type mockEngine struct {
	srv      *http.Server
	sockPath string
	seenPath string // last path the mock observed (version prefix check)
	pingAPI  string // Api-Version header the mock advertises
}

func newMockEngine(t *testing.T, pingAPI string) *mockEngine {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix dinlenemedi: %v", err)
	}
	m := &mockEngine{sockPath: sock, pingAPI: pingAPI}
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Api-Version", m.pingAPI)
		w.Header().Set("Ostype", "linux")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Version": "27.3.1", "ApiVersion": m.pingAPI, "Os": "linux",
		})
	})
	handle := func(body any, status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if b, err := json.Marshal(body); err == nil {
				_, _ = w.Write(b)
			}
		}
	}
	containers := []map[string]any{
		{"Id": "abc123def4567890", "Names": []string{"/web-01"}, "Image": "nginx:1.25", "State": "running", "Status": "Up 2 minutes", "Created": 1730000000},
		{"Id": "fff000111222333", "Names": []string{"/cache"}, "Image": "redis:7", "State": "exited", "Status": "Exited (0)", "Created": 1730001000},
	}
	mux.HandleFunc("/containers/json", handle(containers, http.StatusOK))
	mux.HandleFunc("/containers/abc123def4567890/json", handle(map[string]any{
		"Id": "abc123def4567890", "Name": "/web-01", "Image": "nginx:1.25", "Created": "2026-11-01T10:00:00Z",
		"State": map[string]any{"Status": "running", "Running": true, "Pid": 4242, "ExitCode": 0, "StartedAt": "2026-11-01T10:00:01Z", "FinishedAt": "0001-01-01T00:00:00Z"},
		// Config.Env YOK — adapter sızdırmaz; mock'ta olsa bile yanıt tipinde alan yok.
		"Mounts": []map[string]any{{"Type": "volume", "Source": "/srv/data", "Destination": "/data", "RW": true}},
	}, http.StatusOK))
	mux.HandleFunc("/containers/missing001/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container: missing001"}`))
	})
	mux.HandleFunc("/containers/abc123def4567890/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tail") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var buf []byte
		frame := func(stream byte, payload string) {
			h := make([]byte, 8)
			h[0] = stream
			binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
			buf = append(buf, h...)
			buf = append(buf, []byte(payload)...)
		}
		frame(0, "hello from stdout\n")
		frame(1, "err: disk full\n")
		_, _ = w.Write(buf)
	})
	lifecycle := func(status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}
	}
	mux.HandleFunc("/containers/abc123def4567890/start", lifecycle(http.StatusNoContent))
	mux.HandleFunc("/containers/abc123def4567890/stop", lifecycle(http.StatusConflict)) // 409: already stopped
	mux.HandleFunc("/containers/abc123def4567890/restart", lifecycle(http.StatusNoContent))
	mux.HandleFunc("/images/json", handle([]map[string]any{
		{"Id": "sha256:aaa111", "RepoTags": []string{"nginx:1.25", "nginx:latest"}, "Size": 187654321, "Created": 1729999999},
	}, http.StatusOK))
	mux.HandleFunc("/networks", handle([]map[string]any{
		{"Name": "bridge", "Id": "net000", "Driver": "bridge", "Scope": "local", "Internal": false},
	}, http.StatusOK))
	mux.HandleFunc("/volumes", handle(map[string]any{
		"Volumes": []map[string]any{{"Name": "app-data", "Driver": "local", "Mountpoint": "/var/lib/docker/volumes/app-data/_data", "CreatedAt": "2026-11-01T09:00:00Z"}},
	}, http.StatusOK))
	var apiPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ham yolu kaydet (versiyon öneki kanıtı), sonra mock'a öneksiz ver.
		m.seenPath = r.URL.Path
		r.URL.Path = apiPrefix.ReplaceAllString(r.URL.Path, "")
		mux.ServeHTTP(w, r)
	})
	m.srv = &http.Server{Handler: root, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = m.srv.Serve(ln) }()
	t.Cleanup(func() { _ = m.srv.Close() })
	return m
}

func newTestClient(m *mockEngine) *Client {
	return &Client{Socket: m.sockPath, MinAPI: MinAPI, MaxAPI: MaxAPI}
}

func TestNegotiationClampsAboveMax(t *testing.T) {
	m := newMockEngine(t, "1.99") // daemon newer than client max
	c := newTestClient(m)
	p, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if p.APIVersion != MaxAPI {
		t.Fatalf("pazarlık %q, beklenen clamp %q", p.APIVersion, MaxAPI)
	}
	if !strings.HasPrefix(m.seenPath, "/v1.47/") {
		t.Fatalf("istek versiyon önekli olmalı: %q", m.seenPath)
	}
}

func TestNegotiationRejectsBelowMin(t *testing.T) {
	m := newMockEngine(t, "1.22") // too old — typed refusal, no guessing
	c := newTestClient(m)
	_, err := c.Ping(context.Background())
	if !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("ErrUnsupportedAPI beklenir: %v", err)
	}
}

func TestContainerListParseAndClamp(t *testing.T) {
	m := newMockEngine(t, "1.47")
	c := newTestClient(m)
	got, err := c.ContainersList(context.Background(), 1)
	if err != nil {
		t.Fatalf("ContainersList: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("clamp: %d satır, beklenen 1", len(got))
	}
	if got[0].ID != "abc123def4567890" || got[0].State != "running" || got[0].Image != "nginx:1.25" {
		t.Fatalf("satır çözümü hatalı: %+v", got[0])
	}
}

func TestContainerInspectBoundedSubset(t *testing.T) {
	m := newMockEngine(t, "1.47")
	c := newTestClient(m)
	d, err := c.ContainerInspect(context.Background(), "abc123def4567890")
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}
	if d.Name != "web-01" || !d.State.Running || d.State.Status != "running" {
		t.Fatalf("detail: %+v", d)
	}
	if len(d.Mounts) != 1 || d.Mounts[0].Destination != "/data" {
		t.Fatalf("mounts: %+v", d.Mounts)
	}
	// Secret-redaction invariant: tip üzerinde Env alanı YOK (derleyici kanıtı
	// yerine JSON çıktı kontrolü).
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "Env") {
		t.Fatalf("inspect çıktısında env olmamalı: %s", b)
	}
}

func TestContainerNotFoundTyped(t *testing.T) {
	m := newMockEngine(t, "1.47")
	c := newTestClient(m)
	_, err := c.ContainerInspect(context.Background(), "missing001")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ErrNotFound beklenir: %v", err)
	}
}

func TestLogsDemux(t *testing.T) {
	m := newMockEngine(t, "1.47")
	c := newTestClient(m)
	lc, err := c.ContainerLogs(context.Background(), "abc123def4567890", 100)
	if err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if lc.Stdout != "hello from stdout\n" || lc.Stderr != "err: disk full\n" || lc.Truncated {
		t.Fatalf("demux: %+v", lc)
	}
}

func TestLifecycleStopConflictTyped(t *testing.T) {
	m := newMockEngine(t, "1.47")
	c := newTestClient(m)
	if err := c.ContainerStart(context.Background(), "abc123def4567890"); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := c.ContainerStop(context.Background(), "abc123def4567890", 10)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("409 → ErrConflict beklenir: %v", err)
	}
	if err := c.ContainerRestart(context.Background(), "abc123def4567890", 0); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

func TestRefValidation(t *testing.T) {
	good := []string{"abc123def4567890", "web-01", "cache_2.prod"}
	for _, g := range good {
		if !ValidRef(g) {
			t.Errorf("ValidRef(%q) true beklenir", g)
		}
	}
	bad := []string{"", "-flag", "../etc/passwd", "a/b", "with space", strings.Repeat("a", 200)}
	for _, b := range bad {
		if ValidRef(b) {
			t.Errorf("ValidRef(%q) false beklenir", b)
		}
	}
	imgGood := []string{"nginx:1.25", "ghcr.io/owner/app@sha256:abcd1234", "library/ubuntu"}
	for _, g := range imgGood {
		if !ValidImageRef(g) {
			t.Errorf("ValidImageRef(%q) true beklenir", g)
		}
	}
	imgBad := []string{"", "a..b", "nginx:1.25 "}
	for _, b := range imgBad {
		if b != "" && ValidImageRef(b) {
			t.Errorf("ValidImageRef(%q) false beklenir", b)
		}
	}
}

func TestRemovePreviewInvariants(t *testing.T) {
	// Executed=false değişmezi: hiçbir kombinasyonda true üretilemez (sabit).
	kinds := []string{"container", "image", "volume", "network"}
	for _, k := range kinds {
		p := RenderRemovePreview(k, "hedef01", RemoveParams{Force: true, V: true, Link: true, Prune: true})
		if p.Executed {
			t.Fatalf("%s önizlemesi Executed=true olamaz (ADR-0011)", k)
		}
		if p.Method != "DELETE" || p.Note == "" {
			t.Fatalf("%s önizlemesi eksik: %+v", k, p)
		}
	}
	pc := RenderRemovePreview("container", "web01", RemoveParams{Force: true, V: true})
	if pc.APIPath != "/containers/web01?force=1&v=1" {
		t.Fatalf("api path: %q", pc.APIPath)
	}
	pi := RenderRemovePreview("image", "nginx:1.25", RemoveParams{Prune: true})
	if pi.APIPath != "/images/nginx:1.25?noprune=1" {
		t.Fatalf("image api path: %q", pi.APIPath)
	}
}

// TestClientHasNoRemoveExecutionPath pins the ADR-0011 invariant at the type
// level: *Client exposes NO remove/delete executor method.
func TestClientHasNoRemoveExecutionPath(t *testing.T) {
	typ := reflect.TypeOf(&Client{})
	for _, m := range []string{"ContainerRemove", "ImageRemove", "VolumeRemove", "NetworkRemove", "Delete"} {
		if _, ok := typ.MethodByName(m); ok {
			t.Fatalf("Client %s metodu İÇERMEMELİ (ADR-0011 preview-only)", m)
		}
	}
}

func TestUnavailableSocketTyped(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "yok.sock")}
	_, err := c.Ping(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ErrUnavailable beklenir: %v", err)
	}
}

// ---- yardımcılar ----
