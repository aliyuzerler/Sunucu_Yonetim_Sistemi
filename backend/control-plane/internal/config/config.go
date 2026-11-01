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
