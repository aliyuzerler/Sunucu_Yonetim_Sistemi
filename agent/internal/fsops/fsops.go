// Package fsops implements the Faz 10 scoped file manager adapter, executed
// ONLY in the privileged broker (server-privd, --fs-enable).
//
// Security model (ADR-0014, docs/architecture/db-files.md):
//   - SCOPED ROOTS: the only allowed roots are webops-managed site docroots,
//     derived from the allowlisted site name — a raw filesystem path is
//     NEVER accepted from a payload (criterion 5: the host filesystem is
//     not browsable; the scope is structural, not filter-based).
//   - TRAVERSAL: every path is walked with openat + O_NOFOLLOW from the
//     root directory fd. Symlinks are NEVER followed during traversal
//     (symlink escape and symlink-final-component are typed refusals) —
//     this closes both the static traversal and the TOCTOU race windows.
//   - RACE PROOF: the final opened object is verified through
//     /proc/self/fd/<n>: the KERNEL's view of the real path must still be
//     inside the canonical (symlink-resolved) docroot. An attacker swapping
//     directories mid-operation cannot redirect the opened object out of
//     scope without the proof failing.
//   - SIZE: downloads and uploads are capped (512 KiB raw) — the broker
//     protocol and task result storage are bounded by design; larger files
//     are a typed refusal (honest, no truncation).
//   - MIME/EXTENSION: uploads are checked against a dangerous-extension
//     denylist AND a sniffed-content denylist (http.DetectContentType) —
//     defense in depth against webshell drops into a served docroot; the
//     security boundary remains the root scoping itself.
//   - PERMISSIONS: chmod takes a bounded mode (≤ 0777, setuid/setgid/sticky
//     refused); chown has NO target parameter — it always restores the
//     ownership of the site's own derived system user (webops.SiteUser).
package fsops

import (
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
)

// Operation names (mirror backend/internal/tasks ops_faz10.go).
const (
	OpFSList     = "fs.list"
	OpFSDownload = "fs.download"
	OpFSUpload   = "fs.upload"
	OpFSChmod    = "fs.chmod"
	OpFSChown    = "fs.chown"
)

// Limits (mirrored in backend/internal/tasks ops_faz10.go).
const (
	MaxRelPathBytes = 400
	MaxSegments     = 16
	MaxSegmentBytes = 96
	MaxListEntries  = 500
	// RawFileCap bounds download AND upload payloads (broker line cap and
	// task-result storage stay bounded; base64 inflates by 4/3).
	RawFileCap      = 512 << 10
	MaxPayloadBytes = 768 << 10 // base64-encoded payload bound
	MaxMode         = 0o777
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidRoot = errors.New("geçersiz kök (atanmış site adı değil)")
	ErrInvalidPath = errors.New("geçersiz yol")
	ErrNotFound    = errors.New("dosya/dizin bulunamadı")
	ErrNotSiteRoot = errors.New("kök docroot yok — site yok veya web hosting kullanılmıyor")
	// ErrSymlink reports a symlink blocking the operation (never followed).
	ErrSymlink        = errors.New("sembolik bağlantı üzerinde işlem yapılmaz")
	ErrScope          = errors.New("hedef kök dışına taşıyor")
	ErrTooLarge       = errors.New("dosya boyutu sınırı aşıldı (512 KiB)")
	ErrTooManyEntries = errors.New("dizin çok büyük (500 girdi üstü sayılmaz)")
	ErrExists         = errors.New("dosya zaten var (overwrite=false)")
	ErrForbiddenExt   = errors.New("uzantı politikası reddediyor")
	ErrForbiddenMime  = errors.New("içerik türü politikası reddediyor")
	ErrInvalidMode    = errors.New("geçersiz mod (0..0777, setuid/setgid/sticky yasak)")
	ErrNoSiteUser     = errors.New("site sistemi kullanıcısı yok")
	ErrInvalidPayload = errors.New("geçersiz içerik (base64)")
	ErrUnavailable    = errors.New("dosya ortamı kullanılamıyor")
)

// segmentRE: one path component. Dots-only names are additionally refused
// in ValidRelPath (".", "..").
var segmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,96}$`)

// forbiddenExts: uploads that could become executable content in a served
// docroot (nginx+PHP-FPM executes .php; .ht* is Apache config; keys/certs
// must not be dropped into web roots).
var forbiddenExts = map[string]bool{
	".php": true, ".php3": true, ".php4": true, ".php5": true, ".php7": true,
	".phtml": true, ".phps": true, ".phar": true,
	".cgi": true, ".pl": true, ".py": true, ".sh": true, ".bash": true,
	".zsh": true, ".service": true, ".so": true, ".bin": true,
	".htaccess": true, ".htpasswd": true,
	".key": true, ".pem": true, ".crt": true, ".cer": true,
	".p12": true, ".pfx": true, ".jks": true,
}

// forbiddenMimePrefixes: sniffed content types that must never be uploaded
// regardless of extension (defense in depth — DetectContentType on the
// first bytes).
var forbiddenMimePrefixes = []string{
	"application/x-sh", "application/x-csh", "application/x-elf",
	"application/x-executable", "application/x-sharedlib",
	"application/x-msdownload", "application/x-dosexec",
}

// executableMagic: raw content prefixes of executable/script formats.
// DetectContentType alone is not enough (Go's sniff table has no shebang
// signature — "#!/bin/sh" comes back as text/plain), so the policy checks
// the bytes directly. Belt over DetectContentType's suspenders.
var executableMagic = []string{"#!", "\x7fELF", "MZ", "\xca\xfe\xba\xbe"}

// ValidRoot reports whether the root is an allowlisted site name (the SAME
// allowlist as the web hosting family — one namespace, one truth; the
// docroot is derived only from this name).
func ValidRoot(root string) bool { return webops.ValidSiteName(root) }

// ValidRelPath validates a payload-relative path and splits it into
// segments: relative, no leading/trailing slash, no "." / ".." segments,
// bounded segment count and byte sizes. Empty path = the root itself.
func ValidRelPath(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if len(p) > MaxRelPathBytes {
		return nil, ErrInvalidPath
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return nil, ErrInvalidPath
	}
	segs := strings.Split(p, "/")
	if len(segs) > MaxSegments {
		return nil, ErrInvalidPath
	}
	for _, s := range segs {
		if s == "." || s == ".." {
			return nil, ErrInvalidPath
		}
		if !segmentRE.MatchString(s) {
			return nil, ErrInvalidPath
		}
	}
	return segs, nil
}

// ParseMode parses an octal mode string ("0644") and enforces the bounded
// mask (no setuid/setgid/sticky).
func ParseMode(mode string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(mode), 8, 32)
	if err != nil {
		return 0, ErrInvalidMode
	}
	if v > MaxMode {
		return 0, ErrInvalidMode
	}
	return uint32(v), nil
}

// CheckUploadPolicy enforces the extension + sniffed-MIME policy for one
// upload (name + decoded content). Returns the sniffed MIME for the result.
func CheckUploadPolicy(name string, content []byte) (mime string, err error) {
	ext := strings.ToLower(name)
	if i := strings.LastIndexByte(ext, '.'); i >= 0 {
		ext = ext[i:]
	} else {
		ext = ""
	}
	if forbiddenExts[ext] {
		return "", ErrForbiddenExt
	}
	sniff := content
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	detected := http.DetectContentType(sniff)
	for _, p := range forbiddenMimePrefixes {
		if strings.HasPrefix(detected, p) {
			return detected, ErrForbiddenMime
		}
	}
	for _, m := range executableMagic {
		if strings.HasPrefix(string(sniff), m) {
			return detected, ErrForbiddenMime
		}
	}
	return detected, nil
}

// DecodeContent decodes the base64 payload and enforces RawFileCap BEFORE
// any disk write (fail early, fail bounded).
func DecodeContent(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, ErrInvalidPayload
	}
	if len(b64) > base64.StdEncoding.EncodedLen(RawFileCap) {
		return nil, ErrTooLarge
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, ErrInvalidPayload
	}
	if len(raw) > RawFileCap {
		return nil, ErrTooLarge
	}
	return raw, nil
}
