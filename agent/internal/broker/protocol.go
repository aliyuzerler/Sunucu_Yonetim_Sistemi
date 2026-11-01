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

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/composeops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
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

	// Faz 06: read-only package inventory + firewall survey/preview.
	OpLinuxPkgList             = pkgops.OpLinuxPkgList
	OpLinuxFirewallStatus      = fwops.OpLinuxFirewallStatus
	OpLinuxFirewallRulesList   = fwops.OpLinuxFirewallRulesList
	OpLinuxFirewallRulePreview = fwops.OpLinuxFirewallRulePreview

	// Faz 07: Docker Engine ailesi. Daemon soketine erişim YALNIZ bu
	// broker'dadır (unix socket; TCP yok — ADR-0011). remove ailesi
	// PREVIEW-ONLY'dir: yürütme yolu hiç yoktur.
	OpDockerContainerList    = dockerx.OpDockerContainerList
	OpDockerContainerInspect = dockerx.OpDockerContainerInspect
	OpDockerContainerLogs    = dockerx.OpDockerContainerLogs
	OpDockerContainerStart   = dockerx.OpDockerContainerStart
	OpDockerContainerStop    = dockerx.OpDockerContainerStop
	OpDockerContainerRestart = dockerx.OpDockerContainerRestart
	OpDockerContainerRemove  = dockerx.OpDockerContainerRemove
	OpDockerImageList        = dockerx.OpDockerImageList
	OpDockerImageRemove      = dockerx.OpDockerImageRemove
	OpDockerNetworkList      = dockerx.OpDockerNetworkList
	OpDockerNetworkRemove    = dockerx.OpDockerNetworkRemove
	OpDockerVolumeList       = dockerx.OpDockerVolumeList
	OpDockerVolumeRemove     = dockerx.OpDockerVolumeRemove

	// Faz 07: Compose analizi — broker'dan geçmez (agent-local saf
	// analiz); ad sabitleri dışa açılır.
	OpDockerComposeValidate = composeops.OpDockerComposeValidate
	OpDockerComposeDeploy   = composeops.OpDockerComposeDeploy

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

// dockerCall carries the Faz 07 docker-family parameters extracted at
// validation time (ref/tail/timeout/remove-kind).
type dockerCall struct {
	kind    string // remove-preview kind: container|image|volume|network
	ref     string
	tail    int
	timeout int
	force   bool
	link    bool
	v       bool
	prune   bool
}

// validatedCall is the typed outcome of request validation.
type validatedCall struct {
	target    string              // unit/service name (service families)
	listLimit int                 // list family bound
	preview   fwops.PreviewParams // firewall preview payload
	docker    dockerCall          // docker family payload
}

// validateRequest enforces the allowlist + payload schema for one request.
// It returns a validatedCall and an error with a stable category.
func validateRequest(req *Request) (validatedCall, error) {
	empty := validatedCall{}
	if req == nil {
		return empty, reject(CatInvalidRequest, "boş istek")
	}
	if req.V != ProtocolV1 {
		return empty, reject(CatInvalidRequest, "desteklenmeyen protokol sürümü")
	}
	switch req.Operation {
	case OpServiceStatus, OpServiceStart, OpServiceStop, OpServiceRestart:
		var p UnitParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "unit şeması uyumsuz")
		}
		if err := winops.ValidateLinuxUnit(p.Unit); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{target: p.Unit}, nil
	case OpWinServiceStatus, OpWinServiceStart, OpWinServiceStop, OpWinServiceRestart:
		var p ServiceParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "service şeması uyumsuz")
		}
		if err := winops.ValidateServiceName(p.Service); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{target: p.Service}, nil
	case OpServiceList, OpWinServiceList, OpLinuxPkgList, OpLinuxFirewallRulesList,
		OpDockerContainerList, OpDockerImageList, OpDockerNetworkList, OpDockerVolumeList:
		var p ListParams
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil || p.Limit < 0 || p.Limit > 500 {
			return empty, reject(CatInvalidParams, "list şeması uyumsuz")
		}
		if p.Limit == 0 {
			p.Limit = 200
		}
		return validatedCall{listLimit: p.Limit}, nil
	case OpLinuxFirewallStatus:
		var p struct{}
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "status şeması uyumsuz (boş obje beklenir)")
		}
		return empty, nil
	case OpLinuxFirewallRulePreview:
		var p fwops.PreviewParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "preview şeması uyumsuz")
		}
		if err := fwops.ValidatePreviewParams(p); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{preview: p}, nil

	// ---- Faz 07: docker ailesi ----
	case OpDockerContainerInspect, OpDockerContainerStart:
		var p dockerx.RemoveParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "ref şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{docker: dockerCall{ref: p.Ref}}, nil
	case OpDockerContainerLogs:
		var p struct {
			Ref  string `json:"ref"`
			Tail int    `json:"tail,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "log şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		if p.Tail == 0 {
			p.Tail = 100
		}
		if p.Tail < 1 || p.Tail > 500 {
			return empty, reject(CatInvalidParams, "tail 1..500 olmalı")
		}
		return validatedCall{docker: dockerCall{ref: p.Ref, tail: p.Tail}}, nil
	case OpDockerContainerStop, OpDockerContainerRestart:
		var p struct {
			Ref     string `json:"ref"`
			Timeout int    `json:"timeout,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "action şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		if p.Timeout == 0 {
			p.Timeout = 10
		}
		if p.Timeout < 1 || p.Timeout > 120 {
			return empty, reject(CatInvalidParams, "timeout 1..120 olmalı")
		}
		return validatedCall{docker: dockerCall{ref: p.Ref, timeout: p.Timeout}}, nil
	case OpDockerContainerRemove, OpDockerImageRemove, OpDockerVolumeRemove, OpDockerNetworkRemove:
		kind := removeKind(req.Operation)
		var p dockerx.RemoveParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "remove şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams(kind, p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		// PREVIEW-ONLY (ADR-0011): yalnız render edilecek veri taşınır; broker
		// bu aile için hiçbir DELETE yürütme yolu içermez.
		return validatedCall{docker: dockerCall{kind: kind, ref: p.Ref, force: p.Force, link: p.Link, v: p.V, prune: p.Prune}}, nil
	case "":
		return empty, reject(CatInvalidRequest, "operasyon boş")
	default:
		return empty, reject(CatUnsupportedOp, "operasyon allowlist dışı")
	}
}

// removeKind maps a preview-only remove op to its resource kind.
func removeKind(op string) string {
	switch op {
	case OpDockerContainerRemove:
		return "container"
	case OpDockerImageRemove:
		return "image"
	case OpDockerVolumeRemove:
		return "volume"
	case OpDockerNetworkRemove:
		return "network"
	}
	return ""
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
