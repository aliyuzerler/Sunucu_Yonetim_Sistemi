// Package dockerx implements the Faz 07 Docker Engine adapter. It talks to
// the Docker Engine API over the LOCAL Unix socket (/var/run/docker.sock by
// default) — there is NO TCP path: the socket is reachable only for the
// privileged broker (server-privd), which is the only process allowed to
// dial it (criterion: daemon access stays agent/broker-side).
//
// Security (ADR-0003/ADR-0011):
//   - every request is a typed operation; user-controlled strings are
//     strictly validated refs (no shell anywhere — the Engine API is plain
//     HTTP over a Unix socket);
//   - API version is NEGOTIATED via /_ping and clamped to the supported
//     window [MinAPI, MaxAPI]; older daemons get a typed unsupported error
//     (criterion: version negotiation + unsupported API handling);
//   - all outputs are bounded (1 MiB lists, 64 KiB logs);
//   - DESTRUCTIVE operations are PREVIEW-ONLY: they render the exact Engine
//     API call as DATA and never execute it (ADR-0011, Executed=false
//     invariant — mirrors the firewall preview policy ADR-0010).
package dockerx

import (
	"errors"
	"fmt"
	"regexp"
)

// Operation names (mirror backend/internal/tasks ops_faz07.go).
const (
	OpDockerContainerList    = "docker.container.list"
	OpDockerContainerInspect = "docker.container.inspect"
	OpDockerContainerLogs    = "docker.container.logs"
	OpDockerContainerStart   = "docker.container.start"
	OpDockerContainerStop    = "docker.container.stop"
	OpDockerContainerRestart = "docker.container.restart"
	OpDockerContainerRemove  = "docker.container.remove" // PREVIEW-ONLY
	OpDockerImageList        = "docker.image.list"
	OpDockerImageRemove      = "docker.image.remove" // PREVIEW-ONLY
	OpDockerNetworkList      = "docker.network.list"
	OpDockerNetworkRemove    = "docker.network.remove" // PREVIEW-ONLY
	OpDockerVolumeList       = "docker.volume.list"
	OpDockerVolumeRemove     = "docker.volume.remove" // PREVIEW-ONLY
)

// DefaultSocket is the standard local Engine API endpoint.
const DefaultSocket = "/var/run/docker.sock"

// Supported API window: the client negotiates within [MinAPI, MaxAPI].
// A daemon below MinAPI is REFUSED with ErrUnsupportedAPI (no guessing);
// a daemon above MaxAPI is clamped to MaxAPI (backwards-compatible
// negotiation per Engine docs).
const (
	MinAPI = "1.24"
	MaxAPI = "1.47"
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	// ErrUnsupportedAPI reports a daemon outside the supported API window.
	ErrUnsupportedAPI = errors.New("docker engine api sürümü desteklenmiyor")
	// ErrNotFound reports a missing container/image/volume/network ref.
	ErrNotFound = errors.New("kaynak bulunamadı")
	// ErrConflict reports a state conflict (already running, must-stop-first…).
	ErrConflict = errors.New("docker kaynak durumu çelişkili")
	// ErrEngine reports an Engine API error with its bounded message.
	ErrEngine = errors.New("docker engine hatası")
	// ErrInvalidRef reports a malformed container/image/volume/network ref.
	ErrInvalidRef = errors.New("geçersiz kaynak referansı")
	// ErrLimit reports a bad list/tail bound.
	ErrLimit = errors.New("limit 1..500 olmalı")
	// ErrUnavailable reports that the Engine socket is not reachable.
	ErrUnavailable = errors.New("docker socket erişilemiyor")
)

// refRE: container IDs (64-hex) and container/network/volume names share the
// strict [A-Za-z0-9][A-Za-z0-9_.-] shape; no slashes, no dot-dot traversal,
// no leading dash (URL-path safety by construction).
var refRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// imageRefRE additionally allows ":" (tag), "@" (digest) and "/" (registry
// path) for image references; ".." segments are rejected explicitly.
var imageRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,199}$`)

// ValidRef reports whether the container/volume/network ref is allowed.
func ValidRef(ref string) bool {
	if !refRE.MatchString(ref) {
		return false
	}
	return !hasDotDot(ref)
}

// ValidImageRef reports whether the image reference is allowed.
func ValidImageRef(ref string) bool {
	if !imageRefRE.MatchString(ref) {
		return false
	}
	return !hasDotDot(ref)
}

func hasDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

// ValidateLimit bounds list/tail limits.
func ValidateLimit(limit int) error {
	if limit < 1 || limit > 500 {
		return ErrLimit
	}
	return nil
}

// ContainerSummary is one bounded row of `GET /containers/json`.
type ContainerSummary struct {
	ID        string   `json:"id"`
	Names     []string `json:"names,omitempty"`
	Image     string   `json:"image"`
	State     string   `json:"state,omitempty"`
	Status    string   `json:"status,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// ContainerDetail is the bounded subset of container inspect we expose.
// Config.Env is deliberately OMITTED: environment values frequently carry
// secrets and inspect must not leak them through the control plane
// (documented in docs/architecture/docker-support.md).
type ContainerDetail struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Image   string   `json:"image,omitempty"`
	Created string   `json:"created,omitempty"`
	State   CntState `json:"state"`
	Mounts  []Mount  `json:"mounts,omitempty"`
}

// CntState is the container state subset.
type CntState struct {
	Status     string `json:"status,omitempty"` // created|running|paused|restarting|exited|dead
	Running    bool   `json:"running"`
	Paused     bool   `json:"paused"`
	Restarting bool   `json:"restarting"`
	ExitCode   int    `json:"exit_code,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// Mount is one bounded mount row.
type Mount struct {
	Type        string `json:"type,omitempty"` // bind|volume|tmpfs
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	RW          bool   `json:"rw"`
}

// ImageSummary is one bounded image row.
type ImageSummary struct {
	ID       string   `json:"id"`
	RepoTags []string `json:"repo_tags,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Created  string   `json:"created,omitempty"`
}

// NetworkSummary is one bounded network row.
type NetworkSummary struct {
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Driver   string `json:"driver,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Internal bool   `json:"internal"`
}

// VolumeInfo is one bounded volume row.
type VolumeInfo struct {
	Name       string `json:"name"`
	Driver     string `json:"driver,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// PingInfo is the negotiated engine identity.
type PingInfo struct {
	ServerVersion string `json:"server_version"`
	APIVersion    string `json:"api_version"` // negotiated (client-side window)
	OSType        string `json:"os_type,omitempty"`
}

// LogChunk is a bounded container-logs read (streams demuxed).
type LogChunk struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// RemovePreview is the SAFE result of a destructive op: the exact Engine API
// call (and its CLI equivalent) rendered as data. Executed is ALWAYS false —
// the adapter has no remove execution path at all (ADR-0011).
type RemovePreview struct {
	Kind     string   `json:"kind"`   // container|image|volume|network
	Ref      string   `json:"ref"`    // validated ref echoed back
	Flags    []string `json:"flags"`  // normalized flags (force, v, link, noprune)
	Method   string   `json:"method"` // DELETE
	APIPath  string   `json:"api_path"`
	CLI      string   `json:"cli"`
	Executed bool     `json:"executed"` // documented invariant: always false
	Note     string   `json:"note"`
}

// RemoveParams is the typed payload of the four preview-only remove ops.
type RemoveParams struct {
	Ref   string `json:"ref"`
	Force bool   `json:"force,omitempty"`
	Link  bool   `json:"link,omitempty"`  // container only
	V     bool   `json:"v,omitempty"`     // container only (remove anonymous volumes)
	Prune bool   `json:"prune,omitempty"` // image only (keep dangling children)
}

// ValidateRemoveParams enforces the kind-specific ref shape. Flags carry no
// further constraints (booleans; strict decode rejects unknown fields).
func ValidateRemoveParams(kind, ref string) error {
	if kind == "image" {
		if !ValidImageRef(ref) {
			return fmt.Errorf("%w: image ref biçimi hatalı", ErrInvalidRef)
		}
		return nil
	}
	if !ValidRef(ref) {
		return fmt.Errorf("%w: ref biçimi hatalı", ErrInvalidRef)
	}
	return nil
}

// RenderRemovePreview builds the preview for one destructive op. Pure
// function; APIPath/CLI are DATA for the operator — nothing calls them.
func RenderRemovePreview(kind, ref string, f RemoveParams) RemovePreview {
	var api, cli string
	switch kind {
	case "container":
		cli = "docker rm"
		if f.V {
			cli += " -v"
		}
		if f.Link {
			cli += " --link"
		}
		if f.Force {
			cli += " --force"
		}
		api = "containers/" + ref + "?" + boolQuery("force", f.Force) + boolQuery("v", f.V) + boolQuery("link", f.Link)
	case "image":
		cli = "docker rmi"
		if f.Force {
			cli += " --force"
		}
		if f.Prune {
			cli += " --no-prune"
		}
		api = "images/" + ref + "?" + boolQuery("force", f.Force) + boolQuery("noprune", f.Prune)
	case "volume":
		cli = "docker volume rm"
		if f.Force {
			cli += " --force"
		}
		api = "volumes/" + ref + "?" + boolQuery("force", f.Force)
	case "network":
		cli = "docker network rm"
		if f.Force {
			cli += " --force"
		}
		api = "networks/" + ref + "?" + boolQuery("force", f.Force)
	}
	return RemovePreview{
		Kind:     kind,
		Ref:      ref,
		Flags:    normalizeFlags(kind, f),
		Method:   "DELETE",
		APIPath:  "/" + trimQuery(api),
		CLI:      cli + " " + ref,
		Executed: false,
		Note:     "ÖNİZLEME — hiçbir şey silinmedi. Uygulama operatör onayıyla ayrı adımda yapılır (ADR-0011).",
	}
}

func boolQuery(key string, on bool) string {
	if on {
		return key + "=1&"
	}
	return ""
}

func trimQuery(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '?' || s[len(s)-1] == '&') {
		s = s[:len(s)-1]
	}
	return s
}

func normalizeFlags(kind string, f RemoveParams) []string {
	var out []string
	switch kind {
	case "container":
		if f.Force {
			out = append(out, "force")
		}
		if f.Link {
			out = append(out, "link")
		}
		if f.V {
			out = append(out, "v")
		}
	case "image":
		if f.Force {
			out = append(out, "force")
		}
		if f.Prune {
			out = append(out, "noprune")
		}
	default:
		if f.Force {
			out = append(out, "force")
		}
	}
	return out
}
