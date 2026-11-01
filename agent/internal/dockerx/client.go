package dockerx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Output bounds (survey-style reads, not exports).
const (
	maxListBytes  = 1 << 20 // 1 MiB per list/inspect response
	maxLogBytes   = 64 << 10
	defaultAction = 10 // stop/restart grace seconds
)

// Client is the Engine API client over a local Unix socket. The zero value
// dials /var/run/docker.sock; Socket is injectable for hermetic tests.
type Client struct {
	Socket string
	// API window overrides (empty = package defaults MinAPI/MaxAPI).
	MinAPI string
	MaxAPI string

	mu         sync.Mutex
	negotiated string
	hc         *http.Client
}

// runner interfaces this client satisfies (checked at compile time against
// the broker Config surface in config_unix.go).
func (c *Client) httpClient() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hc == nil {
		socket := c.socketPath()
		c.hc = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
				DisableKeepAlives: true,
			},
		}
	}
	return c.hc
}

func (c *Client) socketPath() string {
	if c.Socket != "" {
		return c.Socket
	}
	return "/var/run/docker.sock"
}

func (c *Client) minAPI() string {
	if c.MinAPI != "" {
		return c.MinAPI
	}
	return MinAPI
}

func (c *Client) maxAPI() string {
	if c.MaxAPI != "" {
		return c.MaxAPI
	}
	return MaxAPI
}

// negotiate pins the API version: GET /_ping (Api-Version header) → clamp to
// [min,max]. Daemon below min → ErrUnsupportedAPI (typed refusal, no
// guessing). The negotiated version is cached for the process lifetime.
func (c *Client) negotiate(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.negotiated != "" {
		out := c.negotiated
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	hc := c.httpClient()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	daemon := resp.Header.Get("Api-Version")
	if daemon == "" {
		return "", fmt.Errorf("%w: Api-Version üst bilgisi yok", ErrUnsupportedAPI)
	}
	neg, err := clampAPI(daemon, c.minAPI(), c.maxAPI())
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.negotiated = neg
	c.mu.Unlock()
	return neg, nil
}

// clampAPI compares major.minor strings numerically.
func clampAPI(daemon, min, max string) (string, error) {
	d, ok := parseAPI(daemon)
	if !ok {
		return "", fmt.Errorf("%w: daemon sürümü okunamadı (%q)", ErrUnsupportedAPI, daemon)
	}
	lo, ok := parseAPI(min)
	if !ok {
		return "", fmt.Errorf("istemci MinAPI hatalı: %q", min)
	}
	hi, ok := parseAPI(max)
	if !ok {
		return "", fmt.Errorf("istemci MaxAPI hatalı: %q", max)
	}
	if d < lo {
		return "", fmt.Errorf("%w: daemon %s < istemci minimum %s", ErrUnsupportedAPI, daemon, min)
	}
	if d > hi {
		return max, nil
	}
	return daemon, nil
}

func parseAPI(v string) (int, bool) {
	major, minor, found := strings.Cut(v, ".")
	if !found {
		return 0, false
	}
	m, err1 := strconv.Atoi(major)
	n, err2 := strconv.Atoi(minor)
	if err1 != nil || err2 != nil || m < 0 || n < 0 {
		return 0, false
	}
	return m*1000 + n, true
}

// do performs one bounded Engine API call against the negotiated version.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, maxBytes int64) ([]byte, int, error) {
	apiVer, err := c.negotiate(ctx)
	if err != nil {
		return nil, 0, err
	}
	u := "http://docker/v" + apiVer + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if readErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: gövde okunamadı: %v", ErrEngine, readErr)
	}
	if int64(len(body)) > maxBytes {
		return body[:maxBytes], resp.StatusCode, fmt.Errorf("%w: çıktı sınırı aşıldı (%d bayt)", ErrEngine, maxBytes)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299, resp.StatusCode == 304:
		return body, resp.StatusCode, nil
	case resp.StatusCode == 404:
		return body, resp.StatusCode, ErrNotFound
	case resp.StatusCode == 409:
		return body, resp.StatusCode, fmt.Errorf("%w: %s", ErrConflict, engineMsg(body))
	default:
		return body, resp.StatusCode, fmt.Errorf("%w: http %d: %s", ErrEngine, resp.StatusCode, engineMsg(body))
	}
}

// engineMsg extracts the bounded {"message": ...} body docker returns.
func engineMsg(body []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &m); err != nil || m.Message == "" {
		s := strings.TrimSpace(string(body))
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	if len(m.Message) > 300 {
		return m.Message[:300]
	}
	return m.Message
}

func limitQuery(limit int) url.Values {
	q := url.Values{}
	q.Set("all", "1")
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return q
}

// Ping returns the engine identity (real negotiation, no cache bypass).
func (c *Client) Ping(ctx context.Context) (PingInfo, error) {
	apiVer, err := c.negotiate(ctx)
	if err != nil {
		return PingInfo{}, err
	}
	body, _, err := c.do(ctx, http.MethodGet, "/version", nil, 64<<10)
	if err != nil {
		return PingInfo{}, err
	}
	var v struct {
		Version    string `json:"Version"`
		ApiVersion string `json:"ApiVersion"`
		Os         string `json:"Os"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return PingInfo{}, fmt.Errorf("%w: /version çözülemedi", ErrEngine)
	}
	return PingInfo{ServerVersion: v.Version, APIVersion: apiVer, OSType: v.Os}, nil
}

// ContainersList returns bounded container summaries.
func (c *Client) ContainersList(ctx context.Context, limit int) ([]ContainerSummary, error) {
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	body, _, err := c.do(ctx, http.MethodGet, "/containers/json", limitQuery(limit), maxListBytes)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		Image   string   `json:"Image"`
		State   string   `json:"State"`
		Status  string   `json:"Status"`
		Created int64    `json:"Created"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: container listesi çözülemedi", ErrEngine)
	}
	out := make([]ContainerSummary, 0, len(raw))
	for _, r := range raw {
		if len(out) >= limit {
			break
		}
		out = append(out, ContainerSummary{
			ID:        r.ID,
			Names:     r.Names,
			Image:     r.Image,
			State:     r.State,
			Status:    r.Status,
			CreatedAt: unixSeconds(r.Created),
		})
	}
	return out, nil
}

// ContainerInspect returns the bounded detail subset.
func (c *Client) ContainerInspect(ctx context.Context, id string) (ContainerDetail, error) {
	if !ValidRef(id) {
		return ContainerDetail{}, fmt.Errorf("%w: %q", ErrInvalidRef, id)
	}
	body, _, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, maxListBytes)
	if err != nil {
		return ContainerDetail{}, err
	}
	var raw struct {
		ID      string `json:"Id"`
		Name    string `json:"Name"`
		Image   string `json:"Image"`
		Created string `json:"Created"`
		State   struct {
			Status     string `json:"Status"`
			Running    bool   `json:"Running"`
			Paused     bool   `json:"Paused"`
			Restarting bool   `json:"Restarting"`
			ExitCode   int    `json:"ExitCode"`
			Error      string `json:"Error"`
			StartedAt  string `json:"StartedAt"`
			FinishedAt string `json:"FinishedAt"`
		} `json:"State"`
		Mounts []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ContainerDetail{}, fmt.Errorf("%w: inspect çözülemedi", ErrEngine)
	}
	d := ContainerDetail{
		ID:      raw.ID,
		Name:    strings.TrimPrefix(raw.Name, "/"),
		Image:   raw.Image,
		Created: raw.Created,
		State: CntState{
			Status: raw.State.Status, Running: raw.State.Running, Paused: raw.State.Paused,
			Restarting: raw.State.Restarting, ExitCode: raw.State.ExitCode, Error: raw.State.Error,
			StartedAt: raw.State.StartedAt, FinishedAt: raw.State.FinishedAt,
		},
	}
	for _, m := range raw.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: m.Type, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	return d, nil
}

// ContainerLogs reads a bounded, demuxed log window (tail=N, both streams).
func (c *Client) ContainerLogs(ctx context.Context, id string, tail int) (LogChunk, error) {
	if !ValidRef(id) {
		return LogChunk{}, fmt.Errorf("%w: %q", ErrInvalidRef, id)
	}
	if err := ValidateLimit(tail); err != nil {
		return LogChunk{}, err
	}
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("tail", strconv.Itoa(tail))
	body, _, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/logs", q, maxLogBytes)
	if err != nil {
		return LogChunk{}, err
	}
	out, trunc := DemuxStream(body, maxLogBytes)
	return out, trunc
}

// ContainerStart starts one container (204; 304 already-started is success).
func (c *Client) ContainerStart(ctx context.Context, id string) error {
	if !ValidRef(id) {
		return fmt.Errorf("%w: %q", ErrInvalidRef, id)
	}
	_, _, err := c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, 4096)
	return err
}

// ContainerStop stops with a grace timeout (seconds, clamped 1..120).
func (c *Client) ContainerStop(ctx context.Context, id string, timeout int) error {
	if !ValidRef(id) {
		return fmt.Errorf("%w: %q", ErrInvalidRef, id)
	}
	q := url.Values{}
	q.Set("t", strconv.Itoa(clampTimeout(timeout)))
	_, _, err := c.do(ctx, http.MethodPost, "/containers/"+id+"/stop", q, 4096)
	return err
}

// ContainerRestart restarts with a grace timeout.
func (c *Client) ContainerRestart(ctx context.Context, id string, timeout int) error {
	if !ValidRef(id) {
		return fmt.Errorf("%w: %q", ErrInvalidRef, id)
	}
	q := url.Values{}
	q.Set("t", strconv.Itoa(clampTimeout(timeout)))
	_, _, err := c.do(ctx, http.MethodPost, "/containers/"+id+"/restart", q, 4096)
	return err
}

// ImagesList returns bounded image summaries (client-side clamp; the Engine
// list endpoint has no server-side limit parameter).
func (c *Client) ImagesList(ctx context.Context, limit int) ([]ImageSummary, error) {
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	body, _, err := c.do(ctx, http.MethodGet, "/images/json", nil, maxListBytes)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Size     int64    `json:"Size"`
		Created  int64    `json:"Created"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: image listesi çözülemedi", ErrEngine)
	}
	out := make([]ImageSummary, 0, len(raw))
	for _, r := range raw {
		if len(out) >= limit {
			break
		}
		out = append(out, ImageSummary{ID: r.ID, RepoTags: r.RepoTags, Size: r.Size, Created: unixSeconds(r.Created)})
	}
	return out, nil
}

// NetworksList returns bounded network summaries.
func (c *Client) NetworksList(ctx context.Context, limit int) ([]NetworkSummary, error) {
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	body, _, err := c.do(ctx, http.MethodGet, "/networks", nil, maxListBytes)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name     string `json:"Name"`
		ID       string `json:"Id"`
		Driver   string `json:"Driver"`
		Scope    string `json:"Scope"`
		Internal bool   `json:"Internal"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: network listesi çözülemedi", ErrEngine)
	}
	out := make([]NetworkSummary, 0, len(raw))
	for _, r := range raw {
		if len(out) >= limit {
			break
		}
		out = append(out, NetworkSummary{Name: r.Name, ID: r.ID, Driver: r.Driver, Scope: r.Scope, Internal: r.Internal})
	}
	return out, nil
}

// VolumesList returns bounded volume summaries.
func (c *Client) VolumesList(ctx context.Context, limit int) ([]VolumeInfo, error) {
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	body, _, err := c.do(ctx, http.MethodGet, "/volumes", nil, maxListBytes)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Volumes []struct {
			Name       string `json:"Name"`
			Driver     string `json:"Driver"`
			Mountpoint string `json:"Mountpoint"`
			CreatedAt  string `json:"CreatedAt"`
		} `json:"Volumes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: volume listesi çözülemedi", ErrEngine)
	}
	out := make([]VolumeInfo, 0, len(raw.Volumes))
	for _, r := range raw.Volumes {
		if len(out) >= limit {
			break
		}
		out = append(out, VolumeInfo{Name: r.Name, Driver: r.Driver, Mountpoint: r.Mountpoint, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

func clampTimeout(t int) int {
	if t <= 0 {
		return defaultAction
	}
	if t > 120 {
		return 120
	}
	return t
}

func unixSeconds(s int64) string {
	if s <= 0 {
		return ""
	}
	return time.Unix(s, 0).UTC().Format(time.RFC3339)
}
