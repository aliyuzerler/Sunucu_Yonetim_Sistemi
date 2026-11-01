package config

import (
	"strings"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	cfg, err := FromEnv(map[string]string{})
	if err != nil {
		t.Fatalf("varsayılanlar yüklenemedi: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogFormat != "json" || cfg.LogLevel != "info" {
		t.Errorf("varsayılan log ayarları hatalı: %q %q", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.DatabaseURLSet {
		t.Error("DATABASE_URL tanımsızken DatabaseURLSet=true olmamalı")
	}
}

func TestFromEnvInvalidLogLevel(t *testing.T) {
	_, err := FromEnv(map[string]string{"CP_LOG_LEVEL": "verbose"})
	if err == nil || !strings.Contains(err.Error(), "CP_LOG_LEVEL") {
		t.Fatalf("CP_LOG_LEVEL doğrulaması beklenmişti, got %v", err)
	}
}

func TestFromEnvInvalidDatabaseURL(t *testing.T) {
	for _, bad := range []string{"mysql://x", "not a url", "postgres://"} {
		if _, err := FromEnv(map[string]string{"CP_DATABASE_URL": bad}); err == nil {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestFromEnvValidDatabaseURL(t *testing.T) {
	cfg, err := FromEnv(map[string]string{"CP_DATABASE_URL": "postgres://app:secret@localhost:5432/server_control?sslmode=disable"})
	if err != nil {
		t.Fatalf("geçerli URL reddedildi: %v", err)
	}
	if !cfg.DatabaseURLSet {
		t.Error("DatabaseURLSet=true beklenirdi")
	}
}

func TestRedactDatabaseURL(t *testing.T) {
	got := RedactDatabaseURL("postgres://app:hunter2@db.local:5432/serverctl")
	if strings.Contains(got, "hunter2") {
		t.Fatalf("şifre maskelenmedi: %q", got)
	}
	want := "postgres://app:*****@db.local:5432/serverctl"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if RedactDatabaseURL("postgres://db.local/serverctl") != "postgres://db.local/serverctl" {
		t.Error("şifresiz URL değişmemeli")
	}
}

func TestRequestBodyLimitValidation(t *testing.T) {
	if _, err := FromEnv(map[string]string{"CP_REQUEST_BODY_LIMIT": "5"}); err == nil {
		t.Error("aşağı sınır altındaki değer reddedilmeli")
	}
	cfg, err := FromEnv(map[string]string{"CP_REQUEST_BODY_LIMIT": "2097152"})
	if err != nil || cfg.RequestBodyLimit != 2097152 {
		t.Errorf("geçerli limit kabul edilmedi: %v %v", cfg.RequestBodyLimit, err)
	}
}
