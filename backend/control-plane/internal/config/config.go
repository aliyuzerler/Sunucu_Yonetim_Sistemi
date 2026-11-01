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
