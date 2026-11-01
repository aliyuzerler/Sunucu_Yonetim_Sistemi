package pkgops

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseRpmQA(t *testing.T) {
	out := "bash\t5.1.8-9.el9\tx86_64\n" +
		"kernel\t5.14.0-503.22.1.el9_5\tx86_64\n" +
		"gpg-pubkey\t69d7c26a-665a5c9d\t\n" + // boş arch (kabul)
		"noise-line-no-tabs\n" + // tek sütun: sessizce atla
		"\n"
	got := ParseRpmQA(out)
	if len(got) != 3 {
		t.Fatalf("3 paket beklenir, %d geldi: %+v", len(got), got)
	}
	if got[0].Name != "bash" || got[0].Version != "5.1.8-9.el9" || got[0].Arch != "x86_64" || got[0].Source != "rpm" {
		t.Fatalf("ilk paket: %+v", got[0])
	}
	if got[2].Arch != "" {
		t.Fatalf("boş arch korunmalı: %+v", got[2])
	}
}

func TestParseDpkgQuery(t *testing.T) {
	out := "bash\t5.2.15-2ubuntu8\tamd64\n" +
		"libc6\t2.39-0ubuntu8.4\tamd64\n"
	got := ParseDpkgQuery(out)
	if len(got) != 2 || got[1].Name != "libc6" || got[1].Source != "dpkg" {
		t.Fatalf("dpkg parse: %+v", got)
	}
}

func TestParseRejectsControlAndOversize(t *testing.T) {
	// Kontrol karakterli alan asla yankılanmaz (log güvenliği).
	out := "ok\t1.0\tamd64\nbad\x1b[31m\t1.0\tamd64\nname\t\x07evil\tamd64\n"
	got := ParseDpkgQuery(out)
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("kontrol karakterli satırlar atlanmalı: %+v", got)
	}
	long := make([]byte, 250)
	for i := range long {
		long[i] = 'a'
	}
	got = ParseRpmQA(string(long) + "\t1.0\n")
	if len(got) != 0 {
		t.Fatalf("aşırı uzun alan atlanmalı: %+v", got)
	}
}

func TestValidateLimit(t *testing.T) {
	if l, err := ValidateLimit(0); err != nil || l != 200 {
		t.Fatalf("0→200: %d %v", l, err)
	}
	if _, err := ValidateLimit(501); !errors.Is(err, ErrLimit) {
		t.Fatalf("501 hata beklenir: %v", err)
	}
	if _, err := ValidateLimit(-1); err == nil {
		t.Fatal("negatif hata beklenir")
	}
	if l, err := ValidateLimit(500); err != nil || l != 500 {
		t.Fatalf("500 kabul: %d %v", l, err)
	}
}

func TestClamp(t *testing.T) {
	in := []PackageInfo{{Name: "a", Version: "1"}, {Name: "b", Version: "2"}, {Name: "c", Version: "3"}}
	if got := Clamp(in, 2); len(got) != 2 || !reflect.DeepEqual(got[1], in[1]) {
		t.Fatalf("clamp: %+v", got)
	}
	if got := Clamp(in, 10); len(got) != 3 {
		t.Fatalf("sınır altı dokunulmaz: %+v", got)
	}
}
