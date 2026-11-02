package logops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestValidateParams(t *testing.T) {
	cases := []struct {
		name    string
		p       SearchParams
		wantErr bool
	}{
		{"boş", SearchParams{}, false},
		{"limit", SearchParams{Limit: 500}, false},
		{"limit aşımı", SearchParams{Limit: 501}, true},
		{"unit geçerli", SearchParams{Unit: "nginx.service"}, false},
		{"unit enjeksiyon", SearchParams{Unit: "--output=x.service"}, true},
		{"unit uzantısız", SearchParams{Unit: "nginx"}, true},
		{"priority", SearchParams{Priority: 3}, false},
		{"priority aşımı", SearchParams{Priority: 8}, true},
		{"since geçerli", SearchParams{Since: "2026-11-02T10:00:00Z"}, false},
		{"since bozuk", SearchParams{Since: "dün"}, true},
		{"contains uzun", SearchParams{Contains: strings.Repeat("a", 257)}, true},
		{"contains kontrol", SearchParams{Contains: "a\nb"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParams(tc.p)
			if tc.wantErr && err == nil {
				t.Fatalf("hata bekleniyordu")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("beklenmeyen hata: %v", err)
			}
		})
	}
}

// fakeJournalctl writes its argv to a file and emits a fixed JSON array —
// proving the fixed argv template and the parse/redact pipeline.
func fakeJournalctl(t *testing.T, output string, exit int) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "journalctl-fake")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + filepath.Join(dir, "argv.txt") + "\n" +
		"cat <<'JSONEOF'\n" + output + "\nJSONEOF\n" +
		"exit " + itoa(exit) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("fake script: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(dir, "argv.txt")) })
	return bin
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestSearchFixedArgv(t *testing.T) {
	journal := `[{"__REALTIME_TIMESTAMP":"1790620800000000","PRIORITY":"3","_SYSTEMD_UNIT":"nginx.service","SYSLOG_IDENTIFIER":"nginx","MESSAGE":"worker exited"}]`
	bin := fakeJournalctl(t, journal, 0)
	r := &Runner{JournalctlPath: bin}
	entries, err := r.Search(context.Background(), SearchParams{
		Unit: "nginx.service", Priority: 3,
		Since: "2026-11-02T10:00:00Z", Until: "2026-11-02T11:00:00Z", Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("1 entry bekleniyordu: %d", len(entries))
	}
	e := entries[0]
	if e.Severity != 3 || e.SeverityName != "err" {
		t.Fatalf("severity eşlenmedi: %+v", e)
	}
	if e.Unit != "nginx.service" || e.Identifier != "nginx" {
		t.Fatalf("alanlar eksik: %+v", e)
	}
	if !strings.Contains(e.Message, "worker exited") {
		t.Fatalf("mesaj: %q", e.Message)
	}
	// Timestamp: µs → RFC3339.
	if e.Timestamp == "" {
		t.Fatalf("timestamp zorunlu")
	}
	if _, err := json.Marshal(entries); err != nil {
		t.Fatalf("JSON'a dönmez: %v", err)
	}
}

func TestSearchContainsFilterAfterRedaction(t *testing.T) {
	journal := `[
		{"__REALTIME_TIMESTAMP":"1790620800000000","PRIORITY":"6","MESSAGE":"login failed password=S3cr3t! for bob"},
		{"__REALTIME_TIMESTAMP":"1790620801000000","PRIORITY":"6","MESSAGE":"cron ran"}
	]`
	bin := fakeJournalctl(t, journal, 0)
	r := &Runner{JournalctlPath: bin}

	// 'S3cr3t' filtresi redaction SONRASI uygulanır: değer [REDACTED]
	// olduğundan filtre eşleşmez → satır düşer (secret probing engeli).
	entries, err := r.Search(context.Background(), SearchParams{Contains: "S3cr3t"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("redacted değer aranarak satır alınmamalı: %+v", entries)
	}

	// Anahtar adı ile arama çalışır ve değer redact edilmiş görünür.
	entries, err = r.Search(context.Background(), SearchParams{Contains: "password"})
	if err != nil {
		t.Fatalf("search2: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("password satırı gelmeli: %d", len(entries))
	}
	if !strings.Contains(entries[0].Message, "[REDACTED]") {
		t.Fatalf("mesaj redact edilmeli: %q", entries[0].Message)
	}
	if strings.Contains(entries[0].Message, "S3cr3t") {
		t.Fatalf("secret sızdı: %q", entries[0].Message)
	}
}

func TestSearchPermissionDenied(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "journalctl-deny")
	script := "#!/bin/sh\nprintf 'Hint: Access denied; see journalctl -h' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("script: %v", err)
	}
	r := &Runner{JournalctlPath: bin}
	if _, err := r.Search(context.Background(), SearchParams{}); err != ErrPermission {
		t.Fatalf("ErrPermission bekleniyordu: %v", err)
	}
}

func TestSearchBinaryMissing(t *testing.T) {
	r := &Runner{JournalctlPath: filepath.Join(t.TempDir(), "yok")}
	_, err := r.Search(context.Background(), SearchParams{})
	if err == nil {
		t.Fatalf("hata bekleniyordu")
	}
	if !strings.Contains(err.Error(), "journalctl") && !strings.Contains(err.Error(), "kullanılamıyor") {
		t.Fatalf("dürüst hata bekleniyordu: %v", err)
	}
}

func TestRedactLine(t *testing.T) {
	cases := []struct{ in, notWant, want string }{
		{"POST /login password=Sup3rSecret host=web", "Sup3rSecret", "password=[REDACTED]"},
		{"db connect failed: password: 'hunter2'", "hunter2", "[REDACTED]"},
		{"Authorization: Bearer abc.def.ghi-token", "abc.def.ghi-token", "[REDACTED]"},
		{"Authorization: Bearer sk_live_123", "sk_live_123", "[REDACTED]"},
		{"token: ghp_abcdefghijklmnopqrstuvwxyz", "ghp_abcdef", "[REDACTED]"},
		{"-----BEGIN RSA PRIVATE KEY----- MIIEpA", "MIIEpA", "[REDACTED]"},
		{"aws key AKIAIOSFODNN7EXAMPLE in log", "AKIAIOSFODNN7EXAMPLE", "[REDACTED]"},
		{"jwt eyJhbGciOiJIUzI1NiIsInR5.eyJzdWIiOiIxMjM0NTY3.FA2n0sign", "eyJhbGciOiJIUzI1NiIsInR5", "[REDACTED]"},
		{"sıradan log satırı içinde password=alıp giden değer", "alıp", "password=[REDACTED]"},
	}
	for _, tc := range cases {
		out := RedactLine(tc.in)
		if tc.notWant != "" && strings.Contains(out, tc.notWant) {
			t.Fatalf("secret kaldı: %q → %q", tc.in, out)
		}
		if tc.want != "" && !strings.Contains(out, tc.want) {
			t.Fatalf("beklenen redaction yok: %q → %q", tc.in, out)
		}
	}
	// İdempotentlik.
	once := RedactLine("password=x token=y")
	twice := RedactLine(once)
	if once != twice {
		t.Fatalf("redaction idempotent olmalı: %q vs %q", once, twice)
	}
}

func TestMessageTextUnion(t *testing.T) {
	// MESSAGE string.
	e, err := parseAndRedact([]byte(`[{"__REALTIME_TIMESTAMP":"1","MESSAGE":"merhaba dünya"}]`), "")
	if err != nil || len(e) != 1 || e[0].Message != "merhaba dünya" {
		t.Fatalf("string message: %+v %v", e, err)
	}
	// MESSAGE array (journald tekrarlı alan).
	e, err = parseAndRedact([]byte(`[{"__REALTIME_TIMESTAMP":"1","MESSAGE":["parça1 ","parça2"]}]`), "")
	if err != nil || len(e) != 1 || !strings.Contains(e[0].Message, "parça1 parça2") {
		t.Fatalf("array message: %+v %v", e, err)
	}
	// Boş çıktı.
	e, err = parseAndRedact([]byte(`[]`), "")
	if err != nil || len(e) != 0 {
		t.Fatalf("boş çıktı: %+v %v", e, err)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := strings.Repeat("ğü", 100) // 400 bytes
	got := truncate(s, 10)
	if len(got) > 13 { // 10 byte + … (3 byte)
		t.Fatalf("truncate sınırı: %d", len(got))
	}
	// Tek satır flatten.
	if got := flatten("a\r\nb\nc\td"); strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("flatten başarısız: %q", got)
	}
}

func TestParseUnknownFieldsIgnored(t *testing.T) {
	journal := `[{"__REALTIME_TIMESTAMP":"1790620800000000","PRIORITY":"6","MESSAGE":"x","_SELINUX_CONTEXT":"system_u","__MONOTONIC_TIMESTAMP":"5"}]`
	e, err := parseAndRedact([]byte(journal), "")
	if err != nil || len(e) != 1 {
		t.Fatalf("bilinmeyen alanlar yoksayılmalı: %+v %v", e, err)
	}
	if !reflect.DeepEqual(e[0].Severity, 6) {
		t.Fatalf("varsayılan severity")
	}
}
