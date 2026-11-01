package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEventValidate(t *testing.T) {
	valid := Event{
		Actor:         "admin",
		Action:        "node.create",
		Result:        ResultSuccess,
		CorrelationID: "corr-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("geçerli event reddedildi: %v", err)
	}

	cases := []struct {
		name  string
		mutat func(*Event)
	}{
		{"actor boş", func(e *Event) { e.Actor = "" }},
		{"actor çok uzun", func(e *Event) { e.Actor = strings.Repeat("a", 129) }},
		{"action biçimi", func(e *Event) { e.Action = "Node Create!" }},
		{"action kısa", func(e *Event) { e.Action = "ok" }},
		{"sonuç geçersiz", func(e *Event) { e.Result = "maybe" }},
		{"correlation boş", func(e *Event) { e.CorrelationID = " " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := valid
			tc.mutat(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("hata bekleniyordu")
			}
		})
	}
}

func TestRedact(t *testing.T) {
	in := map[string]any{
		"username":      "ali",
		"password":      "s3cr3t-pass",
		"PASSWORD":      "yeni-parola",
		"client_secret": "secret-dgr-99",
		"authorization": "Bearer x-91",
		"session_token": "tok-77713",
		"private_key":   "-----BEGIN-77",
		"api_key":       "k-123456",
		"nested": map[string]any{
			"db_password": "pw-5241",
			"keep":        "görünür",
		},
		"list": []any{map[string]any{"secret_value": "gizli-31", "ok": 1}},
	}
	out := Redact(in)

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, banned := range []string{"s3cr3t-pass", "yeni-parola", "Bearer x-91", "tok-77713", "BEGIN-77", "k-123456", "pw-5241", "gizli-31", "secret-dgr-99"} {
		if strings.Contains(s, banned) {
			t.Fatalf("redaction sızdı: %q çıktıda var: %s", banned, s)
		}
	}
	if !strings.Contains(s, "ali") || !strings.Contains(s, "görünür") {
		t.Fatalf("normal alanlar korunmalıydı: %s", s)
	}
	if !strings.Contains(s, redactedPlaceholder) {
		t.Fatalf("redacted yer tutucu bekleniyordu: %s", s)
	}
	// Girdi map'i mutasyona uğramamalı.
	if in["password"] != "s3cr3t-pass" {
		t.Fatalf("girdi map'i değiştirildi")
	}
}

func TestRedactPrimitives(t *testing.T) {
	if got := Redact("plain"); got != "plain" {
		t.Fatalf("stringPrimitive: %v", got)
	}
	if _, ok := Redact(map[string]any{"token": "x"}).(map[string]any)["token"]; !ok {
		t.Fatal("map yapısı korunmalı")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	enc := encodeCursor(123456789)
	dec, err := decodeCursor(enc)
	if err != nil || dec != 123456789 {
		t.Fatalf("cursor round-trip: %v %v", dec, err)
	}
	if _, err := decodeCursor("!!!geçersiz!!!"); err == nil {
		t.Fatal("geçersiz cursor hata vermeli")
	}
}

// Redaction key matchers must be case-insensitive and cover the documented
// patterns; this test guards the exact regex set.
func TestRedactionPattern(t *testing.T) {
	for _, key := range []string{"password", "PASSWORD", "PassWord", "client_secret",
		"SECRET_VALUE", "api_key", "APIKey", "token", "x-auth-token", "private_key", "credential"} {
		if !redactKeyMatch(key) {
			t.Fatalf("%q redact edilmeliydi", key)
		}
	}
	for _, key := range []string{"username", "node_id", "status", "creator", "created_at", "attempt_count"} {
		if redactKeyMatch(key) {
			t.Fatalf("%q redact edilmemeliydi", key)
		}
	}
}
