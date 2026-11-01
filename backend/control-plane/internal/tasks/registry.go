// Package tasks implements the durable task engine: typed operation registry,
// PostgreSQL task + outbox creation in a single transaction, and a leasing
// worker that claims work with FOR UPDATE SKIP LOCKED (docs/architecture/task-lifecycle.md).
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// maxOpTimeout bounds per-execution timeouts (registry validation + worker).
const maxOpTimeout = 5 * time.Minute

// Operation classes per task-lifecycle.md.
type Class string

const (
	ClassReadOnly    Class = "read_only"
	ClassIdempotent  Class = "idempotent_desired_state"
	ClassNonIdem     Class = "non_idempotent"
	ClassDestructive Class = "destructive"
)

// ErrUnsupportedOperation is returned when the operation is not in the registry.
var ErrUnsupportedOperation = errors.New("desteklenmeyen operasyon")

// ErrIdempotencyConflict is returned when the same idempotency key is reused
// with a different operation.
var ErrIdempotencyConflict = errors.New("idempotency key başka operasyona ait")

// Executor executes one typed operation. Implementations must not run shell
// strings; they map to typed, allowlisted actions only.
type Executor interface {
	Execute(ctx context.Context, t *Task) (result map[string]any, output string, err error)
}

// OperationSpec declares one allowlisted typed operation.
type OperationSpec struct {
	Name       string        // namespaced, e.g. "noop.echo"
	Version    string        // semantic contract version
	Class      Class         // retry semantics class
	RetrySafe  bool          // whether the executor may be retried after a lost lease
	Timeout    time.Duration // per-execution timeout
	MaxPayload int           // raw payload size limit in bytes
	Validate   func(payload json.RawMessage) error
	Exec       Executor
}

// Registry holds the allowlisted operations for this control plane build.
type Registry struct {
	ops map[string]*OperationSpec
}

// NewRegistry builds a registry from the given specs (copy-safe).
func NewRegistry(specs ...*OperationSpec) (*Registry, error) {
	r := &Registry{ops: map[string]*OperationSpec{}}
	for _, s := range specs {
		if err := r.validateSpec(s); err != nil {
			return nil, err
		}
		if _, dup := r.ops[s.Name]; dup {
			return nil, fmt.Errorf("tasks: operasyon tekrarlı: %s", s.Name)
		}
		r.ops[s.Name] = s
	}
	return r, nil
}

var opNameRE = regexp.MustCompile(`^[a-z0-9_.]{3,128}$`)

func (r *Registry) validateSpec(s *OperationSpec) error {
	if !opNameRE.MatchString(s.Name) {
		return fmt.Errorf("tasks: operasyon adı geçersiz: %q", s.Name)
	}
	if strings.TrimSpace(s.Version) == "" || len(s.Version) > 32 {
		return fmt.Errorf("tasks: %s sürümü geçersiz", s.Name)
	}
	switch s.Class {
	case ClassReadOnly, ClassIdempotent, ClassNonIdem, ClassDestructive:
	default:
		return fmt.Errorf("tasks: %s sınıfı geçersiz", s.Name)
	}
	if s.Timeout <= 0 || s.Timeout > maxOpTimeout {
		return fmt.Errorf("tasks: %s zaman aşımı 1s..%s olmalı", s.Name, maxOpTimeout)
	}
	if s.MaxPayload <= 0 || s.MaxPayload > 1<<20 {
		return fmt.Errorf("tasks: %s payload limiti geçersiz", s.Name)
	}
	if s.Validate == nil {
		return fmt.Errorf("tasks: %s validator eksik", s.Name)
	}
	if s.Exec == nil {
		return fmt.Errorf("tasks: %s executor eksik", s.Name)
	}
	return nil
}

// Spec returns the spec for an operation name.
func (r *Registry) Spec(name string) (*OperationSpec, bool) {
	s, ok := r.ops[name]
	return s, ok
}

// Names returns sorted operation names (for API metadata).
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.ops))
	for name := range r.ops {
		out = append(out, name)
	}
	// sorted insert
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
