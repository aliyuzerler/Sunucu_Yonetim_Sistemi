// Package config loads and validates control plane configuration from the
// environment. Secrets are never logged; use RedactDatabaseURL whenever the
// connection string must be rendered.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

// Config carries all runtime settings for the control plane process.
type Config struct {
	HTTPAddr         string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	ShutdownGrace    time.Duration
	RequestBodyLimit int64

	LogLevel  string // debug|info|warn|error
	LogFormat string // json|text

	DatabaseURL    string
	DatabaseURLSet bool

	// Identity/session settings (Faz 02).
	CookieSecure bool          // Secure flag on the session cookie (prod TLS: true)
	SessionTTL   time.Duration // server-side session lifetime
	BootstrapTTL time.Duration // one-time admin bootstrap token lifetime

	// Agent protocol settings (Faz 03). CA paths empty → gRPC listener off.
	GRPCAddr string
	CACert   string
	CAKey    string

	// Faz 09: secrets + ACME. SecretsKey empty → DNS provider
	// registrations and cert material are REJECTED (honest config
	// error; no silent fallback). ACME unset → cert ops refuse.
	SecretsKey       string // CP_SECRETS_KEY (base64, 32 bayt)
	ACMEAccountKey   string // CP_ACME_ACCOUNT_KEY (dosya yolu)
	ACMEDirectoryURL string // CP_ACME_DIRECTORY_URL (örn. LE staging)
	ACMEContact      string // CP_ACME_CONTACT (örn. mailto:ops@example.com)

	// Faz 13: metrik yüzeyi + alarm değerlendirici.
	// MetricsToken boş → /metrics Bearer'sız açıktır (yalnız gizli-olmayan
	// agregat seriler; reverse proxy kısıtı önerilir). Dolu → Bearer ZORUNLU.
	MetricsToken    string        // CP_METRICS_TOKEN
	AlertEvalInterv time.Duration // CP_ALERT_EVAL_INTERVAL (10s..1h)

	// Faz 14: kontrol paneli yedeği deposu. Yedek dosyaları bu dizinde
	// tutulur; CP_SECRETS_KEY bu dizine ASLA yazılmaz (kriter 3 —
	// internal/backup.ValidateKeySeparation + dump içerik taraması).
	BackupDir string // CP_BACKUP_DIR (varsayılan /var/backups/server-control/control-plane)

	// Faz 15: eklenti manifest + güncelleme artifact imza güven kökleri
	// (base64 ed25519 genel anahtarları; virgülle ayrık). Boş → ilgili
	// kayıt/yayın yüzeyi KAPALIDIR (fail-closed, ADR-0019/0020).
	ExtensionTrustKeys []string // CP_EXTENSION_TRUST_KEYS
	UpdateTrustKeys    []string // CP_UPDATE_TRUST_KEYS
}

// Load reads configuration from process environment variables.
func Load() (Config, error) {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return FromEnv(env)
}

// FromEnv builds a Config from the provided environment map (testable).
func FromEnv(env map[string]string) (Config, error) {
	cfg := Config{
		HTTPAddr:         valueOr(env, "CP_HTTP_ADDR", ":8080"),
		ReadTimeout:      15 * time.Second,
		WriteTimeout:     15 * time.Second,
		IdleTimeout:      120 * time.Second,
		ShutdownGrace:    20 * time.Second,
		RequestBodyLimit: 1 << 20, // 1 MiB
		LogLevel:         valueOr(env, "CP_LOG_LEVEL", "info"),
		LogFormat:        valueOr(env, "CP_LOG_FORMAT", "json"),
	}

	if v, ok := env["CP_REQUEST_BODY_LIMIT"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1024 || n > 10<<20 {
			return Config{}, fmt.Errorf("CP_REQUEST_BODY_LIMIT geçersiz: %q (1024..10485760)", v)
		}
		cfg.RequestBodyLimit = n
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("CP_LOG_LEVEL geçersiz: %q (debug|info|warn|error)", cfg.LogLevel)
	}
	switch cfg.LogFormat {
	case "json", "text":
	default:
		return Config{}, fmt.Errorf("CP_LOG_FORMAT geçersiz: %q (json|text)", cfg.LogFormat)
	}

	if raw, ok := env["CP_DATABASE_URL"]; ok && strings.TrimSpace(raw) != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
			return Config{}, fmt.Errorf("CP_DATABASE_URL geçersiz: postgres://user:host:port/db biçimi gerekli")
		}
		cfg.DatabaseURL = raw
		cfg.DatabaseURLSet = true
	}

	cfg.CookieSecure = false
	if v, ok := env["CP_COOKIE_SECURE"]; ok && strings.TrimSpace(v) != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			cfg.CookieSecure = true
		case "0", "false", "no", "off":
			cfg.CookieSecure = false
		default:
			return Config{}, fmt.Errorf("CP_COOKIE_SECURE geçersiz: %q (true|false)", v)
		}
	}

	cfg.SessionTTL = 12 * time.Hour
	if v, ok := env["CP_SESSION_TTL"]; ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 5*time.Minute || d > 30*24*time.Hour {
			return Config{}, fmt.Errorf("CP_SESSION_TTL geçersiz: %q (5m..720h)", v)
		}
		cfg.SessionTTL = d
	}

	cfg.BootstrapTTL = 15 * time.Minute
	if v, ok := env["CP_BOOTSTRAP_TTL"]; ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > time.Hour {
			return Config{}, fmt.Errorf("CP_BOOTSTRAP_TTL geçersiz: %q (1m..1h)", v)
		}
		cfg.BootstrapTTL = d
	}

	cfg.GRPCAddr = valueOr(env, "CP_GRPC_ADDR", ":9090")
	cfg.CACert = strings.TrimSpace(env["CP_CA_CERT"])
	cfg.CAKey = strings.TrimSpace(env["CP_CA_KEY"])
	if (cfg.CACert == "") != (cfg.CAKey == "") {
		return Config{}, fmt.Errorf("CP_CA_CERT ve CP_CA_KEY birlikte tanımlanmalı")
	}

	cfg.SecretsKey = strings.TrimSpace(env["CP_SECRETS_KEY"])
	cfg.ACMEAccountKey = strings.TrimSpace(env["CP_ACME_ACCOUNT_KEY"])
	cfg.ACMEDirectoryURL = strings.TrimSpace(env["CP_ACME_DIRECTORY_URL"])
	cfg.ACMEContact = strings.TrimSpace(env["CP_ACME_CONTACT"])
	if (cfg.ACMEAccountKey == "") != (cfg.ACMEDirectoryURL == "") {
		return Config{}, fmt.Errorf("CP_ACME_ACCOUNT_KEY ve CP_ACME_DIRECTORY_URL birlikte tanımlanmalı")
	}
	if len(cfg.SecretsKey) > 0 {
		if _, err := secrets.ParseKey(cfg.SecretsKey); err != nil {
			return Config{}, fmt.Errorf("CP_SECRETS_KEY geçersiz: base64 32 bayt gerekli")
		}
	}

	// Faz 13: metrik token + alarm değerlendirme aralığı.
	cfg.MetricsToken = strings.TrimSpace(env["CP_METRICS_TOKEN"])
	if cfg.MetricsToken != "" && len(cfg.MetricsToken) < 16 {
		return Config{}, fmt.Errorf("CP_METRICS_TOKEN geçersiz: en az 16 karakter gerekli")
	}
	cfg.AlertEvalInterv = time.Minute
	if v, ok := env["CP_ALERT_EVAL_INTERVAL"]; ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 10*time.Second || d > time.Hour {
			return Config{}, fmt.Errorf("CP_ALERT_EVAL_INTERVAL geçersiz: %q (10s..1h)", v)
		}
		cfg.AlertEvalInterv = d
	}

	// Faz 14: kontrol paneli yedeği deposu.
	cfg.BackupDir = valueOr(env, "CP_BACKUP_DIR", "/var/backups/server-control/control-plane")

	// Faz 15: imza güven kökleri. Biçim: virgülle ayrık base64 ed25519
	// genel anahtarları (32 bayt). Boş liste GEÇERLİDİR ama yüzeyi kapatır.
	cfg.ExtensionTrustKeys = splitTrustKeys(env["CP_EXTENSION_TRUST_KEYS"])
	cfg.UpdateTrustKeys = splitTrustKeys(env["CP_UPDATE_TRUST_KEYS"])
	return cfg, nil
}

// RedactDatabaseURL masks the password component of a PostgreSQL URL so it
// can safely appear in logs or diagnostics.
func RedactDatabaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid-url"
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			// Rebuild manually: url.UserPassword escapes '*' as '%2A'.
			out := u.Scheme + "://" + u.User.Username() + ":*****@" + u.Host + u.Path
			if u.RawQuery != "" {
				out += "?" + u.RawQuery
			}
			return out
		}
	}
	return u.String()
}

func valueOr(env map[string]string, key, fallback string) string {
	if v, ok := env[key]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// splitTrustKeys splits a comma-separated trust key list, trimming spaces
// and dropping empty entries ("a, b" → ["a","b"]).
func splitTrustKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
