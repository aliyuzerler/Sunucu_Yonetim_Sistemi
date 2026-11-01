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
	"sync"
	"time"

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

// DefaultRegistry returns the Faz 03 agent executor set.
func DefaultRegistry() (*ExecutorRegistry, error) {
	return NewRegistry(EchoExecutor{})
}

// ---- küçük yardımcılar ----

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
