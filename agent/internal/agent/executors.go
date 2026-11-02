package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/composeops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dbops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/mailops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/tlsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Executor is one typed, allowlisted operation the agent can execute. It
// NEVER receives or runs shell text (ADR-0003).
type Executor interface {
	Info() *agentv1.Capability
	Execute(ctx context.Context, payload []byte) (result []byte, output string, err error)
}

// ExecutorRegistry maps operation names to executors (default deny).
type ExecutorRegistry struct {
	ops map[string]Executor
}

// NewRegistry builds a registry from the given executors.
func NewRegistry(executors ...Executor) (*ExecutorRegistry, error) {
	r := &ExecutorRegistry{ops: map[string]Executor{}}
	for _, e := range executors {
		info := e.Info()
		if _, dup := r.ops[info.GetCapabilityId()]; dup {
			return nil, fmt.Errorf("operasyon tekrarlı: %s", info.GetCapabilityId())
		}
		r.ops[info.GetCapabilityId()] = e
	}
	return r, nil
}

// Lookup returns the executor registered for an operation name.
func (r *ExecutorRegistry) Lookup(op string) (Executor, bool) {
	e, ok := r.ops[op]
	return e, ok
}

// Capabilities returns the reported capability set.
func (r *ExecutorRegistry) Capabilities() []*agentv1.Capability {
	out := make([]*agentv1.Capability, 0, len(r.ops))
	for _, e := range r.ops {
		out = append(out, e.Info())
	}
	return out
}

// Execute validates and runs one OperationRequest, returning a terminal
// result envelope payload. Unknown/unsupported operations are REJECTED
// (never executed, never guessed).
func (r *ExecutorRegistry) Execute(ctx context.Context, nodeID string, op *agentv1.OperationRequest) *agentv1.OperationResult {
	res := &agentv1.OperationResult{TaskId: op.GetTaskId()}
	if op.GetOperationVersion() == "" || len(op.GetPayload()) > maxPayload {
		res.Status = agentv1.OperationResult_STATUS_REJECTED
		res.ErrorCategory = "payload_invalid"
		return res
	}
	exec, ok := r.ops[op.GetOperation()]
	if !ok {
		res.Status = agentv1.OperationResult_STATUS_REJECTED
		res.ErrorCategory = "unsupported_operation"
		return res
	}
	// Idempotency cache: aynı key ile ikinci istek saklanan sonucu döner
	// (task-lifecycle.md: agent sınırlı idempotency sonucu saklar).
	if key := op.GetTaskId(); key != "" {
		if cached, ok := r.cached(key); ok {
			return cached
		}
	}
	result, output, err := exec.Execute(ctx, op.GetPayload())
	if err != nil {
		res.Status = agentv1.OperationResult_STATUS_FAILED
		res.ErrorCategory = "executor_error"
		if len(output) > 4000 {
			output = output[:4000]
		}
		res.Output = output
		return res
	}
	res.Status = agentv1.OperationResult_STATUS_SUCCEEDED
	res.Result = result
	res.Output = output
	if key := op.GetTaskId(); key != "" {
		r.cache(key, res)
	}
	_ = nodeID
	return res
}

const maxPayload = 64 << 10
const idemRetention = 30 * time.Minute
const idemMax = 256

type idemEntry struct {
	res     *agentv1.OperationResult
	expires time.Time
}

// idempotency cache is bounded (size + retention).
var idemMu sync.Mutex
var idemCache = map[string]idemEntry{}

func (r *ExecutorRegistry) cached(key string) (*agentv1.OperationResult, bool) {
	idemMu.Lock()
	defer idemMu.Unlock()
	e, ok := idemCache[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(idemCache, key)
		}
		return nil, false
	}
	return e.res, true
}

func (r *ExecutorRegistry) cache(key string, res *agentv1.OperationResult) {
	idemMu.Lock()
	defer idemMu.Unlock()
	if len(idemCache) >= idemMax {
		// Basit sınır: en eski girişleri at (Faz 03 için yeterli).
		now := time.Now()
		for k, e := range idemCache {
			if now.After(e.expires) {
				delete(idemCache, k)
			}
		}
		if len(idemCache) >= idemMax {
			for k := range idemCache {
				delete(idemCache, k)
				break
			}
		}
	}
	idemCache[key] = idemEntry{res: res, expires: time.Now().Add(idemRetention)}
}

// EchoExecutor implements "agent.echo": the typed proof of the dispatch loop
// without touching the host (Faz 03 — OS değiştirici işlemler Faz 04'te).
type EchoExecutor struct{}

// Info describes the capability.
func (EchoExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   "agent.echo",
		Version:        "1.0.0",
		Available:      true,
		AdapterId:      "builtin",
		AdapterVersion: Version,
	}
}

// Execute echoes the validated payload message.
func (EchoExecutor) Execute(_ context.Context, payload []byte) ([]byte, string, error) {
	var p struct {
		Message string `json:"message"`
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
	}
	if len(p.Message) > 256 {
		return nil, "", fmt.Errorf("message en fazla 256 karakter")
	}
	body, _ := json.Marshal(map[string]any{
		"echo":   p.Message,
		"agent":  Version,
		"nodeOS": runtime.GOOS,
	})
	return body, "echo tamamlandı", nil
}

// DefaultRegistry builds the per-OS executor set (registry_linux.go /
// registry_windows.go): the echo proof plus broker-backed service
// operations and (windows) the direct event-log reader. brokerPath is the
// local privileged broker's platform endpoint; the service capabilities
// report availability by probing that endpoint at report time.

// ---- servis işlemleri (yerel privileged broker üzerinden) ----

// Prober lets an executor refine capability availability at report time
// (docs/architecture/capability-system.md: capability is a runtime contract).
type Prober interface {
	ProbeAvailable(ctx context.Context) (available bool, reason string)
}

// ServiceExecutor executes one allowlisted linux.systemd.service.* operation
// by forwarding the typed request to the local privileged broker (server-privd).
// It NEVER runs anything itself and NEVER sees shell text: the payload is
// schema-validated JSON and the broker speaks typed operations only.
type ServiceExecutor struct {
	op         string
	brokerPath string
}

// NewServiceExecutor builds one broker-backed service executor.
func NewServiceExecutor(op, brokerPath string) *ServiceExecutor {
	return &ServiceExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability is refined by ProbeAvailable.
func (s *ServiceExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   s.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "systemd-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable dials the broker socket (read-only list probe with limit 1).
func (s *ServiceExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(s.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// servicePayload is the agent-side mirror of the broker's unit schema
// (validated on BOTH sides — proto contract).
type servicePayload struct {
	Unit  string `json:"unit"`
	Limit int    `json:"limit,omitempty"`
}

// Execute validates the payload, forwards the typed request to the broker and
// maps the response to an OperationResult payload.
func (s *ServiceExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	req := &broker.Request{Operation: s.op}
	switch {
	case s.op == broker.OpServiceList || s.op == broker.OpWinServiceList:
		var p servicePayload
		if len(payload) > 0 {
			dec := json.NewDecoder(bytes.NewReader(payload))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
			}
		}
		limit := p.Limit
		if limit == 0 {
			limit = 200
		}
		if limit < 1 || limit > 500 {
			return nil, "", fmt.Errorf("limit 1..500 olmalı")
		}
		req.Params, _ = json.Marshal(broker.ListParams{Limit: limit})
	case isWinOp(s.op):
		var p winServicePayload
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
		}
		if err := winops.ValidateServiceName(p.Service); err != nil {
			return nil, "", err
		}
		req.Params, _ = json.Marshal(broker.ServiceParams{Service: p.Service})
	default:
		var p servicePayload
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
		}
		if err := winops.ValidateLinuxUnit(p.Unit); err != nil {
			return nil, "", err
		}
		req.Params, _ = json.Marshal(broker.UnitParams{Unit: p.Unit})
	}
	res, err := broker.NewClient(s.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// isWinOp reports whether the executor targets the windows SCM family.
func isWinOp(op string) bool {
	return strings.HasPrefix(op, "windows.scm.")
}

// SurveyExecutor executes one Faz 06 read-only survey operation
// (linux.pkg.list | linux.firewall.status | linux.firewall.rules.list |
// linux.firewall.rule.preview) by forwarding the typed request to the local
// privileged broker. Read-only by construction: the broker allowlist has NO
// mutating member in this family, and the preview op renders commands as
// data without ever executing them (ADR-0010).
type SurveyExecutor struct {
	op         string
	brokerPath string
}

// NewSurveyExecutor builds one broker-backed survey executor.
func NewSurveyExecutor(op, brokerPath string) *SurveyExecutor {
	return &SurveyExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability is refined by ProbeAvailable.
func (s *SurveyExecutor) Info() *agentv1.Capability {
	adapter := "survey-broker"
	switch {
	case s.op == broker.OpLinuxPkgList:
		adapter = "pkg-broker"
	case strings.HasPrefix(s.op, "linux.firewall."):
		adapter = "fw-broker"
	}
	return &agentv1.Capability{
		CapabilityId:   s.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      adapter,
		AdapterVersion: Version,
	}
}

// ProbeAvailable dials the broker socket (read-only ping).
func (s *SurveyExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(s.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// surveyListPayload is the shared {limit} schema.
type surveyListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// surveyPreviewPayload mirrors fwops.PreviewParams (validated BOTH sides).
type surveyPreviewPayload struct {
	Zone     string `json:"zone,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Action   string `json:"action"`
}

// Execute validates the payload, forwards the typed request to the broker
// and maps the response.
func (s *SurveyExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	req := &broker.Request{Operation: s.op}
	switch s.op {
	case broker.OpLinuxPkgList, broker.OpLinuxFirewallRulesList:
		var p surveyListPayload
		if len(payload) > 0 {
			dec := json.NewDecoder(bytes.NewReader(payload))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
			}
		}
		limit := p.Limit
		if limit == 0 {
			limit = 200
		}
		if limit < 1 || limit > 500 {
			return nil, "", fmt.Errorf("limit 1..500 olmalı")
		}
		req.Params, _ = json.Marshal(broker.ListParams{Limit: limit})
	case broker.OpLinuxFirewallStatus:
		req.Params, _ = json.Marshal(struct{}{})
	case broker.OpLinuxFirewallRulePreview:
		var p surveyPreviewPayload
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
		}
		pp := fwops.PreviewParams{Zone: p.Zone, Port: p.Port, Protocol: p.Protocol, Action: p.Action}
		if err := fwops.ValidatePreviewParams(pp); err != nil {
			return nil, "", err
		}
		req.Params, _ = json.Marshal(pp)
	default:
		return nil, "", fmt.Errorf("survey executor izin dışı operasyon: %s", s.op)
	}
	res, err := broker.NewClient(s.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// ---- Faz 07: Docker Engine + Compose executor'ları ----

// DockerExecutor executes one docker.* operation by forwarding the typed
// request to the local privileged broker. The Engine socket is ONLY reachable
// from the broker (root, unix socket; ADR-0011) — the agent never dials it.
// The remove family is preview-only end to end.
type DockerExecutor struct {
	op           string
	brokerPath   string
	dockerSocket string // probe only (never dialed by the agent)
}

// NewDockerExecutor builds one broker-backed docker executor.
func NewDockerExecutor(op, brokerPath, dockerSocket string) *DockerExecutor {
	return &DockerExecutor{op: op, brokerPath: brokerPath, dockerSocket: dockerSocket}
}

// Info describes the capability; availability refined by ProbeAvailable.
func (d *DockerExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   d.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "docker-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: docker socket var mı (varlık kontrolü — agent sokete
// BAĞLANMAZ) ve broker ayakta mı.
func (d *DockerExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	if st, err := os.Stat(d.dockerSocket); err != nil || st.IsDir() {
		return false, "docker socket bulunamadı (" + d.dockerSocket + ")"
	}
	cli := broker.NewClient(d.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if ok, reason := cli.Ping(probeCtx); !ok {
		return false, reason
	}
	return true, ""
}

// dockerListPayload is the shared {limit} schema of the docker list family.
type dockerListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// dockerLogsPayload mirrors the broker log schema (validated BOTH sides).
type dockerLogsPayload struct {
	Ref  string `json:"ref"`
	Tail int    `json:"tail,omitempty"`
}

// dockerActionPayload mirrors the broker stop/restart schema.
type dockerActionPayload struct {
	Ref     string `json:"ref"`
	Timeout int    `json:"timeout,omitempty"`
}

// Execute validates the payload, forwards the typed request to the broker.
func (d *DockerExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	req := &broker.Request{Operation: d.op}
	switch {
	case d.op == broker.OpDockerContainerList || d.op == broker.OpDockerImageList ||
		d.op == broker.OpDockerNetworkList || d.op == broker.OpDockerVolumeList:
		var p dockerListPayload
		if len(payload) > 0 {
			if err := decodeStrict(payload, &p); err != nil {
				return nil, "", err
			}
		}
		limit := p.Limit
		if limit == 0 {
			limit = 200
		}
		if err := dockerx.ValidateLimit(limit); err != nil {
			return nil, "", err
		}
		req.Params, _ = json.Marshal(broker.ListParams{Limit: limit})
	case d.op == broker.OpDockerContainerLogs:
		var p dockerLogsPayload
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if p.Tail == 0 {
			p.Tail = 100
		}
		if !dockerx.ValidRef(p.Ref) || p.Tail < 1 || p.Tail > 500 {
			return nil, "", fmt.Errorf("ref/tail geçersiz")
		}
		req.Params, _ = json.Marshal(struct {
			Ref  string `json:"ref"`
			Tail int    `json:"tail"`
		}{p.Ref, p.Tail})
	case d.op == broker.OpDockerContainerStop || d.op == broker.OpDockerContainerRestart:
		var p dockerActionPayload
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if p.Timeout == 0 {
			p.Timeout = 10
		}
		if !dockerx.ValidRef(p.Ref) || p.Timeout < 1 || p.Timeout > 120 {
			return nil, "", fmt.Errorf("ref/timeout geçersiz")
		}
		req.Params, _ = json.Marshal(struct {
			Ref     string `json:"ref"`
			Timeout int    `json:"timeout"`
		}{p.Ref, p.Timeout})
	case d.op == broker.OpDockerImageRemove:
		var p dockerx.RemoveParams
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if !dockerx.ValidImageRef(p.Ref) {
			return nil, "", fmt.Errorf("%w: %q", dockerx.ErrInvalidRef, p.Ref)
		}
		req.Params, _ = json.Marshal(p)
	default:
		// inspect/start/remove(container|volume|network): {ref} ailesi.
		var p dockerx.RemoveParams
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if !dockerx.ValidRef(p.Ref) {
			return nil, "", fmt.Errorf("%w: %q", dockerx.ErrInvalidRef, p.Ref)
		}
		req.Params, _ = json.Marshal(p)
	}
	res, err := broker.NewClient(d.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// WebExecutor executes one web.* operation (Faz 08 hosting family) by
// forwarding the typed request to the local privileged broker. ALL real
// mutations (useradd, config writes, nginx/php-fpm reloads) happen ONLY in
// the broker behind --web-enable; the agent merely validates and relays
// (ADR-0012).
type WebExecutor struct {
	op         string
	brokerPath string
}

// NewWebExecutor builds one broker-backed web hosting executor.
func NewWebExecutor(op, brokerPath string) *WebExecutor {
	return &WebExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability refined by ProbeAvailable.
func (w *WebExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   w.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "web-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: broker ayakta mı (web ailesinin tek gerçek bağımlılığı —
// nginx/php-fpm varlığı broker yürütme anında tipli hata verir).
func (w *WebExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(w.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// webSitePayload is the agent-side mirror of the {site} schema.
type webSitePayload struct {
	Site string `json:"site"`
}

// webListPayload is the shared {limit} schema.
type webListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// Execute validates the payload and forwards the typed request.
func (w *WebExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	req := &broker.Request{Operation: w.op}
	switch {
	case w.op == broker.OpWebSiteList:
		var p webListPayload
		if len(payload) > 0 {
			if err := decodeStrict(payload, &p); err != nil {
				return nil, "", err
			}
		}
		limit := p.Limit
		if limit == 0 {
			limit = 200
		}
		if limit < 0 || limit > webops.MaxSiteList {
			return nil, "", fmt.Errorf("%w: limit 1..%d olmalı", webops.ErrLimit, webops.MaxSiteList)
		}
		req.Params, _ = json.Marshal(broker.ListParams{Limit: limit})
	case w.op == broker.OpWebSiteStatus || w.op == broker.OpWebSiteDisable || w.op == broker.OpWebSiteDelete:
		var p webSitePayload
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if !webops.ValidSiteName(p.Site) {
			return nil, "", fmt.Errorf("%w: %q", webops.ErrInvalidName, p.Site)
		}
		req.Params, _ = json.Marshal(p)
	case w.op == broker.OpWebSiteCreate || w.op == broker.OpWebSiteUpdate:
		var spec webops.SiteSpec
		if err := decodeStrict(payload, &spec); err != nil {
			return nil, "", err
		}
		if err := spec.Validate(); err != nil {
			return nil, "", err
		}
		req.Params = payload
	case w.op == broker.OpWebPHPList:
		req.Params = []byte("{}")

	// ---- Faz 09: TLS sertifika ailesi (yalnız public materyal geçer) ----
	case w.op == broker.OpWebCertRequest:
		var p struct {
			Domain string   `json:"domain"`
			SANs   []string `json:"sans,omitempty"`
		}
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		domains, err := tlsops.NormalizeDomains(p.Domain, p.SANs)
		if err != nil {
			return nil, "", err
		}
		out, _ := json.Marshal(map[string]any{"domain": domains[0], "sans": domains[1:]})
		req.Params = out
	case w.op == broker.OpWebCertInstall:
		var p struct {
			Domain   string `json:"domain"`
			CertPEM  string `json:"cert_pem"`
			ChainPEM string `json:"chain_pem,omitempty"`
		}
		if err := decodeStrict(payload, &p); err != nil {
			return nil, "", err
		}
		if !tlsops.ValidDomain(p.Domain) {
			return nil, "", fmt.Errorf("%w: %q", tlsops.ErrInvalidDomain, p.Domain)
		}
		if p.CertPEM == "" || len(p.CertPEM) > tlsops.MaxCertPEMLen || len(p.ChainPEM) > tlsops.MaxCertPEMLen {
			return nil, "", fmt.Errorf("%w: PEM boyut sınırları", tlsops.ErrCertInvalid)
		}
		req.Params = payload
	case w.op == broker.OpWebCertStatus:
		req.Params = []byte("{}")
	default:
		return nil, "", fmt.Errorf("web executor izin dışı operasyon: %s", w.op)
	}
	res, err := broker.NewClient(w.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// ComposeExecutor runs the agent-LOCAL compose analysis (docker.compose.validate
// / docker.compose.deploy). Pure analysis: no daemon, no broker, no execution;
// the deploy op returns a plan with the Executed=false invariant (ADR-0011).
type ComposeExecutor struct {
	op string
}

// NewComposeExecutor builds one compose analysis executor.
func NewComposeExecutor(op string) *ComposeExecutor {
	return &ComposeExecutor{op: op}
}

// Info describes the capability (always available: pure analysis).
func (c *ComposeExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   c.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "builtin-compose",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: compose analizi saf işlevdir — her zaman kullanılabilir.
func (c *ComposeExecutor) ProbeAvailable(context.Context) (bool, string) {
	return true, ""
}

// composePayload is the agent-side mirror of the compose envelope.
type composePayload struct {
	Compose string `json:"compose"`
}

// Execute validates the envelope and runs the pure analysis.
func (c *ComposeExecutor) Execute(_ context.Context, payload []byte) ([]byte, string, error) {
	var p composePayload
	if err := decodeStrict(payload, &p); err != nil {
		return nil, "", err
	}
	if err := composeops.ValidatePayload(composeops.ComposePayload{Compose: p.Compose}); err != nil {
		return nil, "", err
	}
	switch c.op {
	case composeops.OpDockerComposeValidate:
		rep := composeops.Validate(p.Compose)
		b, err := json.Marshal(rep)
		if err != nil {
			return nil, "", err
		}
		return b, "compose analizi tamamlandı", nil
	case composeops.OpDockerComposeDeploy:
		plan := composeops.BuildDeployPlan(p.Compose)
		b, err := json.Marshal(plan)
		if err != nil {
			return nil, "", err
		}
		return b, "compose planı üretildi (yürütülmedi — ADR-0011)", nil
	default:
		return nil, "", fmt.Errorf("compose executor izin dışı operasyon: %s", c.op)
	}
}

// winServicePayload is the agent-side mirror of the windows service schema.
type winServicePayload struct {
	Service string `json:"service"`
	Limit   int    `json:"limit,omitempty"`
}

func now() *timestamppb.Timestamp { return timestamppb.New(time.Now().UTC()) }

func jitter(base time.Duration) time.Duration {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return time.Duration(b[0]%100) * base / 100
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func osFamily() string { return runtime.GOOS }

func osVersion() string { return runtime.GOOS + " " + runtime.GOARCH }

// decodeStrict decodes JSON disallowing unknown fields and trailing data.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("payload çözülemedi: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("payload sonrası veri")
	}
	return nil
}

// DbExecutor relays one db.* operation (Faz 10 database family) to the
// local privileged broker. The agent does NOT interpret DB payloads: the
// control-plane registry validator and the broker's strict schemas both
// fully re-validate; this relay only enforces the transport byte cap and
// never touches the node-local database sockets itself (ADR-0014).
type DbExecutor struct {
	op         string
	brokerPath string
}

// NewDbExecutor builds one broker-backed database executor.
func NewDbExecutor(op, brokerPath string) *DbExecutor {
	return &DbExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability refined by ProbeAvailable.
func (d *DbExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   d.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "db-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: broker ayakta mı (istemci varlığı yürütme anında tipli
// hata verir — tahmin yok).
func (d *DbExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(d.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// Execute relays the payload (byte-capped) to the broker.
func (d *DbExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	if len(payload) > dbops.MaxPayloadBytes {
		return nil, "", fmt.Errorf("payload %d bayt > %d sınırı", len(payload), dbops.MaxPayloadBytes)
	}
	req := &broker.Request{Operation: d.op, Params: payload}
	res, err := broker.NewClient(d.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// FsExecutor relays one fs.* operation (Faz 10 scoped file manager) to the
// privileged broker. The relay adds NO path logic — scoping/validation is
// enforced by the control plane validator AND the broker; the byte cap
// keeps the transport honest (fs.upload ≤ 768 KiB, ADR-0014).
type FsExecutor struct {
	op         string
	brokerPath string
}

// NewFsExecutor builds one broker-backed file-manager executor.
func NewFsExecutor(op, brokerPath string) *FsExecutor {
	return &FsExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability refined by ProbeAvailable.
func (f *FsExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   f.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "fs-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: broker ayakta mı.
func (f *FsExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(f.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// Execute relays the payload (byte-capped) to the broker.
func (f *FsExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	capBytes := fsops.MaxPayloadBytes
	if f.op != broker.OpFSUpload {
		capBytes = 4 << 10
	}
	if len(payload) > capBytes {
		return nil, "", fmt.Errorf("payload %d bayt > %d sınırı", len(payload), capBytes)
	}
	req := &broker.Request{Operation: f.op, Params: payload}
	res, err := broker.NewClient(f.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}

// MailExecutor relays one mail.* operation (Faz 11 e-mail family) to the
// privileged broker. The relay adds NO mail logic — validation is enforced
// by the control-plane validator AND the broker; the byte cap keeps the
// transport honest (ADR-0015).
type MailExecutor struct {
	op         string
	brokerPath string
}

// NewMailExecutor builds one broker-backed mail executor.
func NewMailExecutor(op, brokerPath string) *MailExecutor {
	return &MailExecutor{op: op, brokerPath: brokerPath}
}

// Info describes the capability; availability refined by ProbeAvailable.
func (m *MailExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   m.op,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "mail-broker",
		AdapterVersion: Version,
	}
}

// ProbeAvailable: broker ayakta mı (varlık kanıtı; posta bileşenlerinin
// kurulu olup olmadığı mail.preflight'ın KENDİ işidir — tahmin yok).
func (m *MailExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	cli := broker.NewClient(m.brokerPath)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cli.Ping(probeCtx)
}

// Execute relays the payload (byte-capped) to the broker.
func (m *MailExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	if len(payload) > mailops.MaxPayloadBytes {
		return nil, "", fmt.Errorf("payload %d bayt > %d sınırı", len(payload), mailops.MaxPayloadBytes)
	}
	req := &broker.Request{Operation: m.op, Params: payload}
	res, err := broker.NewClient(m.brokerPath).Call(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("broker erişilemiyor: %w", err)
	}
	switch res.Status {
	case broker.StatusSucceeded:
		return res.Result, "broker işlemi tamamlandı", nil
	case broker.StatusRejected:
		return nil, res.Output, fmt.Errorf("broker reddetti: %s", res.ErrorCategory)
	default:
		return nil, res.Output, fmt.Errorf("broker işlemi başarısız: %s", res.ErrorCategory)
	}
}
