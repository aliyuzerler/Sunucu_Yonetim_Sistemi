package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildArtifact constructs a valid SCP-BACKUP-V1 artifact for parser tests
// (row counts patched exactly like dumpToFile does).
func buildArtifact(t *testing.T, tables map[string][]string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString(FormatMarker + "\n")
	b.WriteString("format=copy\n")
	b.WriteString("created_at=2026-11-03T00:00:00Z\n")
	b.WriteString("tables=" + itoa(len(tables)) + "\n\n")
	total := 0
	for name, rows := range tables {
		b.WriteString("TABLE " + name + " rows=" + padRows(int64(len(rows))) + "\n")
		for _, row := range rows {
			b.WriteString(row + "\n")
			total++
		}
		b.WriteString("ENDTABLE\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	b.WriteString("FOOTER tables=" + itoa(len(tables)) + " rows=" + itoa(total) + " sha256=" + hex.EncodeToString(sum[:]) + "\n")
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func padRows(n int64) string {
	s := itoa(int(n))
	for len(s) < rowsFieldWidth {
		s = "0" + s
	}
	return s
}

func TestVerifyStreamValid(t *testing.T) {
	art := buildArtifact(t, map[string][]string{
		`"public"."users"`: {"1\tadmin\t2026-01-01", "2\top\t2026-01-02"},
		`"public"."nodes"`: {"aa\tweb01"},
	})
	vr, err := verifyStream(strings.NewReader(string(art)))
	if err != nil {
		t.Fatalf("verifyStream: %v", err)
	}
	if vr.tables != 2 || vr.rows != 3 {
		t.Fatalf("tablo/satır sayısı: %+v", vr)
	}
	if len(vr.sha) != 64 {
		t.Fatalf("sha uzunluğu: %q", vr.sha)
	}
}

func TestVerifyStreamRejectsCorruption(t *testing.T) {
	valid := buildArtifact(t, map[string][]string{`"public"."users"`: {"1\tadmin"}})
	cases := []struct {
		name string
		mut  func() string
	}{
		{"marker yok", func() string { return strings.Replace(string(valid), FormatMarker, "OTHER", 1) }},
		{"sha bozuk", func() string {
			i := strings.LastIndex(string(valid), "sha256=")
			if i < 0 {
				return string(valid)
			}
			return string(valid)[:i+len("sha256=")] + "00000000" + string(valid)[i+len("sha256=")+8:]
		}},
		{"footer sayısı", func() string {
			// Tablo satır sayacını boz → footer satır sayısı uyuşmaz.
			return strings.Replace(string(valid), " rows="+padRows(1), " rows="+padRows(2), 1)
		}},
		{"ENDTABLE eksik", func() string {
			return strings.Replace(string(valid), "ENDTABLE\nFOOTER", "FOOTER", 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.mut()
			if _, err := verifyStream(strings.NewReader(s)); err == nil {
				t.Fatalf("bozuk artifact reddedilmeli: %s", tc.name)
			}
		})
	}
}

func TestVerifyStreamFooterShaMismatch(t *testing.T) {
	art := buildArtifact(t, map[string][]string{`"public"."users"`: {"1\tadmin"}})
	// Veri satırını değiştir → footer sha uyuşmaz.
	s := strings.Replace(string(art), "1\tadmin", "9\tadmin", 1)
	if _, err := verifyStream(strings.NewReader(s)); err == nil {
		t.Fatal("sha uyuşmazlığı reddedilmeli")
	}
}

func TestValidateKeySeparation(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	// İçeride → red.
	inside := filepath.Join(repo, "key.pem")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeySeparation(repo, inside); !errors.Is(err, ErrKeyInsideRepo) {
		t.Fatalf("repo içi anahtar reddedilmeli: %v", err)
	}
	// Alt dizinde → red.
	sub := filepath.Join(repo, "sub", "key.pem")
	if err := os.MkdirAll(filepath.Dir(sub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeySeparation(repo, sub); !errors.Is(err, ErrKeyInsideRepo) {
		t.Fatalf("repo alt dizinindeki anahtar reddedilmeli: %v", err)
	}
	// Kardeş dizin → kabul.
	sibling := filepath.Join(dir, "keys", "key.pem")
	if err := ValidateKeySeparation(repo, sibling); err != nil {
		t.Fatalf("repo dışı anahtar kabul edilmeli: %v", err)
	}
	// Boş yol → no-op.
	if err := ValidateKeySeparation(repo, ""); err != nil {
		t.Fatalf("boş yol no-op olmalı: %v", err)
	}
}

func TestValidateBackupID(t *testing.T) {
	if err := ValidateBackupID("0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"); err != nil {
		t.Fatalf("geçerli UUID reddedildi: %v", err)
	}
	for _, bad := range []string{"", "zort", "0b7fd4d2_1234_4c2b_9a53_2b6f8f8a1111", "0B7FD4D2-1234-4C2B-9A53-2B6F8F8A1111"} {
		if err := ValidateBackupID(bad); err == nil {
			t.Fatalf("geçersiz id reddedilmeli: %q", bad)
		}
	}
}

func TestValidateDrillDBName(t *testing.T) {
	if err := validateDrillDBName("scp_drill_deadbeef", "server_control"); err != nil {
		t.Fatalf("geçerli tatbikat adı reddedildi: %v", err)
	}
	for _, bad := range []string{"server_control", "scp_drill_XYZ", "scp_drill_deadbeefextra", "postgres", "scp_drill_"} {
		if err := validateDrillDBName(bad, "server_control"); err == nil {
			t.Fatalf("geçersiz hedef reddedilmeli: %q", bad)
		}
	}
	// Canlı db adı kuralca şekle uymasa da ayrıca reddedilir.
	if err := validateDrillDBName("server_control", "server_control"); err == nil {
		t.Fatal("canlı db adı tatbikat hedefi olamaz")
	}
}

func TestContainsPatternBoundary(t *testing.T) {
	// Desen parça sınırlarına bölünse de yakalanmalı.
	key := "TOPSECRETKETBASE64"
	data := strings.Repeat("A", 300<<10) + key + strings.Repeat("B", 300<<10)
	if !containsPattern(strings.NewReader(data), []byte(key)) {
		t.Fatal("bölünmüş desen yakalanmalı")
	}
	if containsPattern(strings.NewReader(strings.Repeat("A", 700<<10)), []byte(key)) {
		t.Fatal("olmayan desen bildirilmemeli")
	}
}

func TestParseHelpers(t *testing.T) {
	name, rows, ok := parseTableHeader(`TABLE "public"."users" rows=0000000000000003`)
	if !ok || name != `"public"."users"` || rows != 3 {
		t.Fatalf("parseTableHeader: %v %v %v", name, rows, ok)
	}
	if _, _, ok := parseTableHeader("TABLE public.users rows=abc"); ok {
		t.Fatal("bozuk header reddedilmeli")
	}
	if _, _, ok := parseTableHeader(`TABLE "public"."users" rows=3`); ok {
		t.Fatal("sabit-genişlik olmayan sayaç reddedilmeli")
	}
	t2, r2, sha, ok := parseFooter("FOOTER tables=2 rows=5 sha256=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	if !ok || t2 != 2 || r2 != 5 || len(sha) != 64 {
		t.Fatalf("parseFooter: %v %v %q %v", t2, r2, sha, ok)
	}
}

func TestQuoteQualifiedNameRejectsInjection(t *testing.T) {
	if quoteQualifiedName(`"public"."users"`) == "" {
		t.Fatal("geçerli ad reddedildi")
	}
	for _, bad := range []string{`public.users`, `"public"."users; drop"`, `"public"`, `"p"."u"."x"`, `"public"."users" extra`} {
		if quoteQualifiedName(bad) != "" {
			t.Fatalf("enjeksiyon biçimi reddedilmeli: %q", bad)
		}
	}
}

func TestLiveDBNameAndDrillURL(t *testing.T) {
	if got := liveDBName("postgres://u:p@localhost:5432/server_control?sslmode=disable"); got != "server_control" {
		t.Fatalf("liveDBName: %q", got)
	}
	u, err := drillURL("postgres://u:p@localhost:5432/server_control?sslmode=disable", "scp_drill_deadbeef")
	if err != nil || !strings.Contains(u, "/scp_drill_deadbeef") {
		t.Fatalf("drillURL: %q %v", u, err)
	}
	if _, err := drillURL("http://x/y", "scp_drill_deadbeef"); err == nil {
		t.Fatal("http şeması reddedilmeli")
	}
}
