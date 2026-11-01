package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 08: Web hosting (nginx + PHP-FPM) ----
//
// Şemalar agent/internal/webops ile AYNA şeklinde tutulur (payload'lar iki
// tarafta da strict doğrulanır — ADR-0012):
//   - site adı allowlist'lidir ve türetilen her şey (kullanıcı, config yolu,
//     docroot) yalnız bu addan gelir; docroot override YOKTUR.
//   - create/update GERÇEK yürütme (broker'da, --web-enable arkasında):
//     atomic yazım + marker + rollback + nginx -t kapısı.
//   - delete dosya-koruyucudur (docroot ASLA silinmez; yalnız kendi
//     marker'lı artifact'ları + türetilen sistem kullanıcısı kaldırılır).
//   - list/status/php.list salt-okuma.

// webSiteNameRE mirrors webops siteNameRE.
var webSiteNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$`)

// webServerNameLabelRE mirrors webops labelRE (per-DNS-label).
var webServerNameLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// webLoopback4RE / webLoopback6RE / webUnixSockRE mirror webops target REs.
var (
	webLoopback4RE = regexp.MustCompile(`^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}:[0-9]{1,5}$`)
	webLoopback6RE = regexp.MustCompile(`^\[::1\]:[0-9]{1,5}$`)
	webUnixSockRE  = regexp.MustCompile(`^unix:/run/[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
)

// WebPHPSupported mirrors webops.PHPSupported (closed matrix).
var WebPHPSupported = []string{"8.2", "8.3", "8.4", "8.5"}

const (
	WebMaxServerNames  = 8
	WebMaxChildrenMax  = 200
	WebMaxChildrenDflt = 10
	WebListenPortDflt  = 80
	MaxWebPayloadBytes = 8 << 10
)

// webValidSiteName mirrors webops.ValidSiteName.
func webValidSiteName(name string) bool { return webSiteNameRE.MatchString(name) }

// webValidServerName mirrors webops.ValidServerName (lowercase labels).
func webValidServerName(host string) bool {
	if host == "" || len(host) > 253 || hasWebDotDot(host) {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !webServerNameLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}

func hasWebDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

// webValidPHPVersion mirrors webops.ValidatePHPVersion.
func webValidPHPVersion(v string) bool {
	for _, ok := range WebPHPSupported {
		if v == ok {
			return true
		}
	}
	return false
}

// webValidProxyTarget mirrors webops.ValidProxyTarget (loopback/unix only).
func webValidProxyTarget(t string) bool {
	if webLoopback4RE.MatchString(t) {
		return webValidLoopback4(t)
	}
	if webLoopback6RE.MatchString(t) {
		_, port, ok := strings.Cut(t, "]:")
		return ok && webValidPort(port)
	}
	if webUnixSockRE.MatchString(t) {
		return !hasWebDotDot(t)
	}
	return false
}

func webValidLoopback4(t string) bool {
	host, port, ok := strings.Cut(t, ":")
	if !ok || !webValidPort(port) {
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
			return false // yalnız loopback (SSRF guard)
		}
	}
	return true
}

func webValidPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}

// webSiteSpec is the mirror of webops.SiteSpec (strict decode).
type webSiteSpec struct {
	Name        string   `json:"name"`
	ServerNames []string `json:"server_names"`
	Backend     string   `json:"backend"`
	PHPVersion  string   `json:"php_version,omitempty"`
	ListenPort  int      `json:"listen_port,omitempty"`
	ProxyTarget string   `json:"proxy_target,omitempty"`
	MaxChildren int      `json:"max_children,omitempty"`
}

// webSitePayload is the {site} schema (status/disable/delete).
type webSitePayload struct {
	Site string `json:"site"`
}

// validateWebSpec enforces the full spec (mirror of webops.SiteSpec.Validate).
func validateWebSpec(s webSiteSpec) error {
	if !webValidSiteName(s.Name) {
		return fmt.Errorf("site adı [a-z0-9][a-z0-9-] kalıbına uymuyor (1..24): %q", s.Name)
	}
	if len(s.ServerNames) == 0 || len(s.ServerNames) > WebMaxServerNames {
		return fmt.Errorf("server_names 1..%d olmalı", WebMaxServerNames)
	}
	seen := map[string]bool{}
	for _, h := range s.ServerNames {
		if !webValidServerName(h) || seen[h] {
			return fmt.Errorf("sunucu adı geçersiz/tekrarlı: %q", h)
		}
		seen[h] = true
	}
	port := s.ListenPort
	if port == 0 {
		port = WebListenPortDflt
	}
	if port < 1 || port > 65535 {
		return errors.New("dinleme portu 1..65535 olmalı")
	}
	switch s.Backend {
	case "static":
		if s.PHPVersion != "" || s.ProxyTarget != "" {
			return errors.New("static backend php_version/proxy_target almaz")
		}
	case "php":
		if s.ProxyTarget != "" {
			return errors.New("php backend proxy_target almaz")
		}
		if !webValidPHPVersion(s.PHPVersion) {
			return fmt.Errorf("php sürümü desteklenen matris dışında: %q", s.PHPVersion)
		}
		mc := s.MaxChildren
		if mc == 0 {
			mc = WebMaxChildrenDflt
		}
		if mc < 1 || mc > WebMaxChildrenMax {
			return fmt.Errorf("max_children 1..%d olmalı", WebMaxChildrenMax)
		}
	case "proxy":
		if s.PHPVersion != "" || s.MaxChildren != 0 {
			return errors.New("proxy backend php alanları almaz")
		}
		if !webValidProxyTarget(s.ProxyTarget) {
			return errors.New("proxy_target yalnız 127.0.0.1:port, [::1]:port veya unix:/run/... olabilir")
		}
	default:
		return errors.New("backend static|php|proxy olmalı")
	}
	return nil
}

// ValidateWebSiteList enforces {} or {"limit": 0..500}.
func ValidateWebSiteList(payload json.RawMessage) error {
	var p struct {
		Limit int `json:"limit,omitempty"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > 500 {
		return errors.New("limit 0..500 olmalı")
	}
	return nil
}

// ValidateWebSiteName enforces the {site} schema.
func ValidateWebSiteName(payload json.RawMessage) error {
	var p webSitePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !webValidSiteName(p.Site) {
		return errors.New("site adı allowlist dışı")
	}
	return nil
}

// ValidateWebSiteSpec enforces the full create/update spec.
func ValidateWebSiteSpec(payload json.RawMessage) error {
	if len(payload) > MaxWebPayloadBytes {
		return fmt.Errorf("payload %d bayt > %d sınırı", len(payload), MaxWebPayloadBytes)
	}
	var p webSiteSpec
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	return validateWebSpec(p)
}

// ValidateWebPHPList enforces {} (empty object).
func ValidateWebPHPList(payload json.RawMessage) error {
	var p struct{}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	return nil
}

// buildFaz08Registry returns the Faz 08 operation specs.
func buildFaz08Registry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name: "web.site.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWebSiteList,
			RequiredPerm: rbac.PermWebRead,
		},
		{
			Name: "web.site.status", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWebSiteName,
			RequiredPerm: rbac.PermWebRead,
		},
		{
			Name: "web.php.list", Version: "1.0.0",
			// PHP sürüm matrisi + kurulu tespit + pool envanteri (kriter 4).
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWebPHPList,
			RequiredPerm: rbac.PermWebRead,
		},
		{
			Name: "web.site.create", Version: "1.0.0",
			// İstenen son durum "site bu configle var" → tekrar çalıştırma
			// yakınsar (kullanıcı/docroot yeniden kullanılır, içerik aynıysa
			// yazım yok) → idempotent + retry-safe dürüsttür.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 5 * time.Minute, MaxPayload: MaxWebPayloadBytes,
			Validate:     ValidateWebSiteSpec,
			RequiredPerm: rbac.PermWebControl,
		},
		{
			Name: "web.site.update", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 5 * time.Minute, MaxPayload: MaxWebPayloadBytes,
			Validate:     ValidateWebSiteSpec,
			RequiredPerm: rbac.PermWebControl,
		},
		{
			Name: "web.site.disable", Version: "1.0.0",
			// Tersine çevrilebilir (config .disabled olarak saklanır) →
			// idempotent; ikinci disable dürüst no-op döner.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 3 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateWebSiteName,
			RequiredPerm: rbac.PermWebControl,
		},
		{
			Name: "web.site.delete", Version: "1.0.0",
			// non_idempotent + retry-güvensiz (systemd/SCM restart kalıbı):
			// ikinci delete "bulunamadı" ile biter. Docroot korunur
			// (ADR-0012); rollback kopyaları her kaldırılan dosya için tutulur.
			Class: ClassNonIdem, Target: TargetNode, RetrySafe: false,
			Timeout: 5 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateWebSiteName,
			RequiredPerm: rbac.PermWebControl,
		},
	}
}
