// Package broker implements the Faz 04 local privileged broker protocol.
//
// Trust model (docs/architecture/security-boundaries.md):
//   - The broker (server-privd) is a SEPARATE binary and identity running as
//     root; the agent runs unprivileged.
//   - The broker listens ONLY on a mode-0660 Unix socket. There is no TCP.
//   - Peer identity is verified with SO_PEERCRED against an explicit UID
//     allowlist; default deny.
//   - Requests are typed JSON with allowlisted operations; parameters are
//     strictly validated. Inputs are NEVER merged into shell strings.
//   - Every accepted request (and every rejection) is audited.
package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// Protocol version of the Unix-socket request/response format.
const ProtocolV1 = 1

// Allowlisted operations (the ONLY ones the broker will ever run). Linux
// and Windows families coexist in the protocol; the per-OS executor
// rejects the foreign family at dispatch (default deny).
const (
	OpServiceList    = "linux.systemd.service.list"
	OpServiceStatus  = "linux.systemd.service.status"
	OpServiceStart   = "linux.systemd.service.start"
	OpServiceStop    = "linux.systemd.service.stop"
	OpServiceRestart = "linux.systemd.service.restart"

	OpWinServiceList    = winops.OpWinServiceList
	OpWinServiceStatus  = winops.OpWinServiceStatus
	OpWinServiceStart   = winops.OpWinServiceStart
	OpWinServiceStop    = winops.OpWinServiceStop
	OpWinServiceRestart = winops.OpWinServiceRestart
)

// Result statuses.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusRejected  = "rejected"
)

// Error categories (bounded, stable).
const (
	CatInvalidRequest   = "invalid_request"
	CatPeerDenied       = "peer_denied"
	CatUnsupportedOp    = "unsupported_operation"
	CatInvalidParams    = "invalid_params"
	CatRateLimited      = "rate_limited"
	CatInternal         = "internal_error"
	CatExecutorFailed   = "executor_error"
	CatExecutorTimedOut = "executor_timeout"
)

// Request is one typed broker request (newline-delimited JSON, one per conn).
type Request struct {
	V         int             `json:"v"`                 // ProtocolV1
	Operation string          `json:"operation"`         // allowlisted op
	TaskID    string          `json:"task_id,omitempty"` // control-plane task
	RequestID string          `json:"request_id"`        // caller correlation
	CorrID    string          `json:"correlation_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Response is the broker's typed reply.
type Response struct {
	V             int             `json:"v"`
	Operation     string          `json:"operation"`
	TaskID        string          `json:"task_id,omitempty"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	ErrorCategory string          `json:"error_category,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Output        string          `json:"output,omitempty"`
}

// UnitParams is the schema for unit-targeted params (status/start/stop/restart).
type UnitParams struct {
	Unit string `json:"unit"`
}

// ServiceParams is the windows schema for name-targeted params (mirror of
// winops.ServiceParams).
type ServiceParams struct {
	Service string `json:"service"`
}

// ListParams is the schema for list params (empty object or with limit).
type ListParams struct {
	Limit int `json:"limit,omitempty"`
}

// maxRequestBytes bounds a single request line.
const maxRequestBytes = 16 << 10

// validateRequest enforces the allowlist + payload schema for one request.
// It returns (unit, listLimit) and an error with a stable category.
func validateRequest(req *Request) (target string, listLimit int, err error) {
	if req == nil {
		return "", 0, reject(CatInvalidRequest, "boş istek")
	}
	if req.V != ProtocolV1 {
		return "", 0, reject(CatInvalidRequest, "desteklenmeyen protokol sürümü")
	}
	switch req.Operation {
	case OpServiceStatus, OpServiceStart, OpServiceStop, OpServiceRestart:
		var p UnitParams
		if err := strictDecode(req.Params, &p); err != nil {
			return "", 0, reject(CatInvalidParams, "unit şeması uyumsuz")
		}
		if err := winops.ValidateLinuxUnit(p.Unit); err != nil {
			return "", 0, reject(CatInvalidParams, err.Error())
		}
		return p.Unit, 0, nil
	case OpWinServiceStatus, OpWinServiceStart, OpWinServiceStop, OpWinServiceRestart:
		var p ServiceParams
		if err := strictDecode(req.Params, &p); err != nil {
			return "", 0, reject(CatInvalidParams, "service şeması uyumsuz")
		}
		if err := winops.ValidateServiceName(p.Service); err != nil {
			return "", 0, reject(CatInvalidParams, err.Error())
		}
		return p.Service, 0, nil
	case OpServiceList, OpWinServiceList:
		var p ListParams
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil || p.Limit < 0 || p.Limit > 500 {
			return "", 0, reject(CatInvalidParams, "list şeması uyumsuz")
		}
		if p.Limit == 0 {
			p.Limit = 200
		}
		return "", p.Limit, nil
	case "":
		return "", 0, reject(CatInvalidRequest, "operasyon boş")
	default:
		return "", 0, reject(CatUnsupportedOp, "operasyon allowlist dışı")
	}
}

// strictDecode decodes JSON disallowing unknown fields and trailing data.
func strictDecode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("sonrası veri")
	}
	return nil
}

type rejectError struct {
	category string
	msg      string
}

func reject(category, msg string) error { return rejectError{category, msg} }

func (e rejectError) Error() string { return e.msg }

// Category extracts a stable error category from an error.
func Category(err error) string {
	var re rejectError
	if errors.As(err, &re) {
		return re.category
	}
	return CatInternal
}

// requestIDRE bounds identifiers echoed into audit records.
var requestIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// sanitizeID clamps and validates echo fields for audit safety.
func sanitizeID(s string) string {
	if requestIDRE.MatchString(s) {
		return s
	}
	return fmt.Sprintf("invalid:%d", len(s))
}
