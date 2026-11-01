// Package webops implements the Faz 08 web hosting adapter (nginx + PHP-FPM).
//
// Security model (ADR-0012, docs/architecture/web-hosting.md):
//   - Execution happens ONLY in the privileged broker (server-privd) via
//     fixed argv (useradd/userdel/nginx/systemctl) — never a shell.
//   - The family is opt-in: `server-privd --web-enable` (default OFF);
//     nil runner → ops rejected as unsupported.
//   - Site names are strictly allowlisted and are the ONLY user input used
//     to derive paths/users; everything else is a closed enum or bounded
//     integer. No document-root override (path traversal by construction).
//   - Config writes are atomic (temp+rename), carry a management marker,
//     refuse to overwrite foreign files, and keep a rollback copy.
//   - nginx config is syntax-checked (`nginx -t`) and inclusion-proved
//     (`nginx -T`) BEFORE any reload; failure restores the rollback copy.
//   - site.delete NEVER removes the document root (user data preserved);
//     it removes only our own marker-bearing artifacts and the site's
//     system user, and keeps rollback copies of everything it removes.
package webops

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Operation names (mirror backend/internal/tasks ops_faz08.go).
const (
	OpWebSiteList    = "web.site.list"
	OpWebSiteStatus  = "web.site.status"
	OpWebSiteCreate  = "web.site.create"
	OpWebSiteUpdate  = "web.site.update"
	OpWebSiteDisable = "web.site.disable"
	OpWebSiteDelete  = "web.site.delete"
	OpWebPHPList     = "web.php.list"
)

// MarkerHeader is the first line of EVERY config file webops writes. Files
// under our managed namespace WITHOUT this marker are foreign and are
// refused (never blindly overwritten — acceptance criterion 5).
const MarkerHeader = "# server-control managed web config — DO NOT EDIT (web.site.*)"

// metaSite/metaBackend/... carry machine-readable metadata in the marker
// block so `list` can report state without re-parsing server blocks.
const (
	metaSite    = "# site: "
	metaBackend = "# backend: "
	metaPHP     = "# php: "
	metaPort    = "# port: "
)

// Backends (closed enum). "proxy" is the Node.js/Python app answer: nginx
// reverse-proxies to a strictly loopback-only target; the app runtime is
// operator-managed (documented boundary — ADR-0012).
const (
	BackendStatic = "static"
	BackendPHP    = "php"
	BackendProxy  = "proxy"
)

// PHPSupported is the CLOSED pool-generation matrix. Versions outside this
// set are refused with ErrPHPUnsupported (no guessing); being IN the set is
// still not enough — the version must also be installed on the node
// (detected, never assumed).
var PHPSupported = []string{"8.2", "8.3", "8.4", "8.5"}

// UserPrefix + ConfPrefix namespace everything webops owns.
const (
	UserPrefix = "sc-web-"
	ConfPrefix = "srvctl-"
	// DisabledSuffix marks a disabled site config (not matched by the
	// nginx `*.conf` include glob; content and rollback stay intact).
	DisabledSuffix = ".disabled"
	// RollbackSuffix is the always-kept previous version of a config file.
	RollbackSuffix = ".rollback"
)

// Limits (bounded by design; mirrored in backend/internal/tasks ops_faz08.go).
const (
	MaxServerNames  = 8
	MaxNameLen      = 24 // user = UserPrefix+name must stay ≤ 31 (useradd NAME_REGEX)
	MaxSiteList     = 500
	MaxChildrenMax  = 200
	MaxChildrenDflt = 10
	ListenPortDflt  = 80
	MaxPayloadBytes = 8 << 10
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	// ErrInvalidName reports a site name outside the allowlist.
	ErrInvalidName = errors.New("geçersiz site adı")
	// ErrInvalidSpec reports an invalid payload field (server_names,
	// backend, php version, port, proxy target, pool bounds).
	ErrInvalidSpec = errors.New("geçersiz site özellikleri")
	// ErrNotFound reports a site that does not exist.
	ErrNotFound = errors.New("site bulunamadı")
	// ErrConflict reports a desired-state conflict (site exists, name/user
	// collision with a non-managed entry).
	ErrConflict = errors.New("site durumu çelişkili")
	// ErrForeignConfig reports a file in our namespace WITHOUT our marker —
	// webops never touches it (no blind overwrite).
	ErrForeignConfig = errors.New("yabancı config dosyası (marker yok) — dokunulmaz")
	// ErrPortConflict reports the requested listen port is owned by another
	// (non-nginx) listener; the message names the evidence found.
	ErrPortConflict = errors.New("dinleme portu çakışması")
	// ErrPHPUnsupported reports a php_version outside PHPSupported.
	ErrPHPUnsupported = errors.New("php sürümü desteklenen matris dışında")
	// ErrPHPNotInstalled reports a matrix version whose FPM is not detected.
	ErrPHPNotInstalled = errors.New("php-fpm sürümü node'da kurulu değil")
	// ErrSyntaxCheck reports `nginx -t` failure; the rollback copy was
	// restored and NO reload happened.
	ErrSyntaxCheck = errors.New("nginx syntax check başarısız — reload yapılmadı")
	// ErrNotIncluded reports the written config is not present in `nginx -T`
	// dump (include path not effective on this node).
	ErrNotIncluded = errors.New("config nginx tarafından include edilmiyor")
	// ErrUnavailable reports the web family cannot run (nginx missing…).
	ErrUnavailable = errors.New("web sunucu ortamı kullanılamıyor")
	// ErrLimit reports a bad list bound.
	ErrLimit = errors.New("limit 1..500 olmalı")
)

// siteNameRE: strict, lowercase, DNS-label-shaped; ≤24 chars so the derived
// system user (sc-web-<name>) fits useradd's 32-char NAME_REGEX with room.
// Leading/trailing dashes are rejected (shell-flag and label hygiene).
var siteNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$`)

// unixTargetRE: proxy targets — loopback IPv4:port, [::1]:port, or a unix
// socket strictly under /run/ (SSRF guard: NO arbitrary hosts, ADR-0012).
var (
	loopback4RE = regexp.MustCompile(`^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}:[0-9]{1,5}$`)
	loopback6RE = regexp.MustCompile(`^\[::1\]:[0-9]{1,5}$`)
	unixSockRE  = regexp.MustCompile(`^unix:/run/[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
)

// labelRE matches one DNS label inside ValidateServerName.
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidSiteName reports whether the site name is in the allowlist.
func ValidSiteName(name string) bool { return siteNameRE.MatchString(name) }

// SiteUser derives the per-site system user (isolation: one user per site).
func SiteUser(name string) string { return UserPrefix + name }

// ValidServerName validates one lowercase hostname (RFC-1123-shaped labels,
// ≤253 total). Wildcards/underscores are deliberately NOT accepted.
func ValidServerName(host string) bool {
	if host == "" || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !labelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidatePHPVersion enforces the closed matrix membership.
func ValidatePHPVersion(v string) error {
	for _, ok := range PHPSupported {
		if v == ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %q (desteklenen: %s)", ErrPHPUnsupported, v, strings.Join(PHPSupported, ", "))
}

// ValidProxyTarget enforces the loopback/unix-socket-only target shape.
func ValidProxyTarget(t string) bool {
	if loopback4RE.MatchString(t) {
		return validLoopback4(t) && !hasDotDot(t)
	}
	if loopback6RE.MatchString(t) {
		_, port, ok := strings.Cut(t, "]:")
		return ok && validLoopbackPort(port) // "[::1]:PORT"
	}
	if unixSockRE.MatchString(t) {
		return !hasDotDot(t)
	}
	return false
}

// validLoopback4 checks octets (≤255) and port (≤65535) of "127.x.y.z:port".
func validLoopback4(t string) bool {
	host, port, ok := strings.Cut(t, ":")
	if !ok || !validLoopbackPort(port) {
		return false
	}
	octets := strings.Split(host, ".")
	if len(octets) != 4 {
		return false
	}
	for i, o := range octets {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 || n > 255 {
			return false
		}
		if i == 0 && n != 127 {
			return false // loopback ONLY (SSRF guard)
		}
	}
	return true
}

// validLoopbackPort bounds the port part of a target.
func validLoopbackPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// ValidatePort bounds the listen port.
func ValidatePort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%w: dinleme portu 1..65535 olmalı", ErrInvalidSpec)
	}
	return nil
}

// SiteSpec is the typed create/update payload (mirrored in ops_faz08.go).
type SiteSpec struct {
	Name        string   `json:"name"`
	ServerNames []string `json:"server_names"`
	Backend     string   `json:"backend"`
	PHPVersion  string   `json:"php_version,omitempty"`
	ListenPort  int      `json:"listen_port,omitempty"`
	ProxyTarget string   `json:"proxy_target,omitempty"`
	MaxChildren int      `json:"max_children,omitempty"`
}

// Validate checks the whole spec; backend-dependent fields are enforced
// (php backend requires a matrix php_version; proxy requires a validated
// target; static rejects both).
func (s SiteSpec) Validate() error {
	if !ValidSiteName(s.Name) {
		return fmt.Errorf("%w: %q (kalıp [a-z0-9][a-z0-9-]{0,23})", ErrInvalidName, s.Name)
	}
	if len(s.ServerNames) == 0 || len(s.ServerNames) > MaxServerNames {
		return fmt.Errorf("%w: server_names 1..%d olmalı", ErrInvalidSpec, MaxServerNames)
	}
	seen := map[string]bool{}
	for _, h := range s.ServerNames {
		if !ValidServerName(h) || seen[h] {
			return fmt.Errorf("%w: sunucu adı %q geçersiz/tekrarlı", ErrInvalidSpec, h)
		}
		seen[h] = true
	}
	port := s.ListenPort
	if port == 0 {
		port = ListenPortDflt
	}
	if err := ValidatePort(port); err != nil {
		return err
	}
	switch s.Backend {
	case BackendStatic:
		if s.PHPVersion != "" || s.ProxyTarget != "" {
			return fmt.Errorf("%w: static backend php_version/proxy_target almaz", ErrInvalidSpec)
		}
	case BackendPHP:
		if s.ProxyTarget != "" {
			return fmt.Errorf("%w: php backend proxy_target almaz", ErrInvalidSpec)
		}
		if err := ValidatePHPVersion(s.PHPVersion); err != nil {
			return err
		}
		mc := s.MaxChildren
		if mc == 0 {
			mc = MaxChildrenDflt
		}
		if mc < 1 || mc > MaxChildrenMax {
			return fmt.Errorf("%w: max_children 1..%d olmalı", ErrInvalidSpec, MaxChildrenMax)
		}
	case BackendProxy:
		if s.PHPVersion != "" || s.MaxChildren != 0 {
			return fmt.Errorf("%w: proxy backend php alanları almaz", ErrInvalidSpec)
		}
		if !ValidProxyTarget(s.ProxyTarget) {
			return fmt.Errorf("%w: proxy_target yalnız 127.0.0.1:port, [::1]:port veya unix:/run/... olabilir", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("%w: backend static|php|proxy olmalı", ErrInvalidSpec)
	}
	return nil
}

// Normalize returns a spec with defaults applied (port, max_children).
func (s SiteSpec) Normalize() SiteSpec {
	if s.ListenPort == 0 {
		s.ListenPort = ListenPortDflt
	}
	if s.Backend == BackendPHP && s.MaxChildren == 0 {
		s.MaxChildren = MaxChildrenDflt
	}
	return s
}

// SiteInfo is one row of web.site.list.
type SiteInfo struct {
	Name       string   `json:"name"`
	Backend    string   `json:"backend,omitempty"`
	PHPVersion string   `json:"php_version,omitempty"`
	Port       int      `json:"port,omitempty"`
	Enabled    bool     `json:"enabled"`
	User       string   `json:"user,omitempty"`
	UserExists bool     `json:"user_exists"`
	DocRoot    string   `json:"docroot,omitempty"`
	DocExists  bool     `json:"docroot_exists"`
	ServerName []string `json:"server_names,omitempty"`
}

// SiteStatus is the detailed web.site.status result.
type SiteStatus struct {
	Site          SiteInfo `json:"site"`
	ConfigPath    string   `json:"config_path,omitempty"`
	RollbackPath  string   `json:"rollback_path,omitempty"`
	HasRollback   bool     `json:"has_rollback"`
	PoolPath      string   `json:"pool_path,omitempty"`
	PoolInstalled bool     `json:"pool_installed"`
	Listener      string   `json:"listener,omitempty"` // port ownership evidence
}

// SiteResult is the create/update/disable/delete outcome. Executed=true is
// set ONLY when the requested mutation actually happened; safety no-ops are
// reported honestly (idempotent re-runs keep Executed=false).
type SiteResult struct {
	Site         string   `json:"site"`
	Action       string   `json:"action"`
	Executed     bool     `json:"executed"`
	ConfigPath   string   `json:"config_path,omitempty"`
	RollbackPath string   `json:"rollback_path,omitempty"`
	DocRoot      string   `json:"docroot,omitempty"`
	DocPreserved bool     `json:"docroot_preserved,omitempty"` // delete: files kept
	User         string   `json:"user,omitempty"`
	PHPVersion   string   `json:"php_version,omitempty"`
	PoolPath     string   `json:"pool_path,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// PHPMatrix is the web.php.list result: closed matrix vs detected reality.
type PHPMatrix struct {
	Supported []string   `json:"supported"`
	Installed []PHPState `json:"installed"`
	Pools     []PoolInfo `json:"pools,omitempty"`
}

// PHPState is one installed FPM version with its unit name.
type PHPState struct {
	Version string `json:"version"`
	Unit    string `json:"unit"`
	PoolDir string `json:"pool_dir"`
}

// PoolInfo is one managed pool file found on the node.
type PoolInfo struct {
	Site    string `json:"site"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

func hasDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

// itoa is a tiny helper so render code reads cleanly.
func itoa(i int) string { return strconv.Itoa(i) }
