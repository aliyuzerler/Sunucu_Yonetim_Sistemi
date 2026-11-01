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
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
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
