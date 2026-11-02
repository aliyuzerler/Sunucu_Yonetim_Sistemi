package tasks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 08 validator pozitif + negatif setleri (ops_faz08.go aynaları) ----

func TestWebValidSiteName(t *testing.T) {
	valid := []string{"a", "blog", "my-site-1"}
	invalid := []string{"", "Blog", "-lead", "trail-", "a_b", "a b", "a/b", strings.Repeat("a", 25)}
	for _, n := range valid {
		if !webValidSiteName(n) {
			t.Errorf("webValidSiteName(%q) = false", n)
		}
	}
	for _, n := range invalid {
		if webValidSiteName(n) {
			t.Errorf("webValidSiteName(%q) = true, yanlış", n)
		}
	}
}

func TestWebValidServerName(t *testing.T) {
	valid := []string{"example.com", "localhost", "a.b.c"}
	invalid := []string{"", "EXAMPLE.com", "-a.com", "a..b", "a_b.c", "a.", strings.Repeat("a", 64) + ".com"}
	for _, h := range valid {
		if !webValidServerName(h) {
			t.Errorf("webValidServerName(%q) = false", h)
		}
	}
	for _, h := range invalid {
		if webValidServerName(h) {
			t.Errorf("webValidServerName(%q) = true, yanlış", h)
		}
	}
}

func TestWebValidProxyTarget(t *testing.T) {
	valid := []string{"127.0.0.1:3000", "[::1]:8080", "unix:/run/app/gunicorn.sock"}
	invalid := []string{
		"10.0.0.5:3000", "0.0.0.0:80", "example.com:80", "127.0.0.1:99999",
		"unix:/tmp/app.sock", "unix:/run/../etc/passwd", "", "http://127.0.0.1:3000",
	}
	for _, s := range valid {
		if !webValidProxyTarget(s) {
			t.Errorf("webValidProxyTarget(%q) = false", s)
		}
	}
	for _, s := range invalid {
		if webValidProxyTarget(s) {
			t.Errorf("webValidProxyTarget(%q) = true, yanlış", s)
		}
	}
}

func TestValidateWebSiteList(t *testing.T) {
	if err := ValidateWebSiteList(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidateWebSiteList(json.RawMessage(`{"limit":100}`)); err != nil {
		t.Errorf("limit 100 geçerli: %v", err)
	}
	if err := ValidateWebSiteList(json.RawMessage(`{"limit":501}`)); err == nil {
		t.Error("limit 501 reddedilmeli")
	}
	if err := ValidateWebSiteList(json.RawMessage(`{"x":1}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateWebSiteName(t *testing.T) {
	if err := ValidateWebSiteName(mustJSON(t, webSitePayload{Site: "blog"})); err != nil {
		t.Errorf("blog geçerli: %v", err)
	}
	invalid := []string{"", "-flag", "../etc", "Blog", "a b", strings.Repeat("a", 30)}
	for _, site := range invalid {
		if err := ValidateWebSiteName(mustJSON(t, webSitePayload{Site: site})); err == nil {
			t.Errorf("site %q KABUL EDİLDİ", site)
		}
	}
	if err := ValidateWebSiteName(json.RawMessage(`{"site":"ok","force":true}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateWebSiteSpec(t *testing.T) {
	positives := []string{
		`{"name":"blog","server_names":["blog.example.com"],"backend":"static"}`,
		`{"name":"shop","server_names":["s.com","s2.com"],"backend":"php","php_version":"8.3","max_children":20}`,
		`{"name":"app","server_names":["a.com"],"backend":"proxy","proxy_target":"127.0.0.1:3000"}`,
		`{"name":"app","server_names":["a.com"],"backend":"proxy","proxy_target":"unix:/run/app.sock"}`,
		`{"name":"web","server_names":["w.com"],"backend":"static","listen_port":8080}`,
	}
	for i, p := range positives {
		if err := ValidateWebSiteSpec(json.RawMessage(p)); err != nil {
			t.Errorf("pos[%d] reddedildi: %v", i, err)
		}
	}
	negatives := []string{
		`{"name":"Blog","server_names":["b.com"],"backend":"static"}`,
		`{"name":"ok","server_names":[],"backend":"static"}`,
		`{"name":"ok","server_names":["b.com","b.com"],"backend":"static"}`,
		`{"name":"ok","server_names":["bad_host.com"],"backend":"static"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"cgi"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"php"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"php","php_version":"7.4"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"php","php_version":"8.3","max_children":999}`,
		`{"name":"ok","server_names":["b.com"],"backend":"php","php_version":"8.3","proxy_target":"127.0.0.1:1"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"proxy"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"proxy","proxy_target":"10.1.1.1:80"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"static","php_version":"8.3"}`,
		`{"name":"ok","server_names":["b.com"],"backend":"static","listen_port":70000}`,
		`{"name":"ok","server_names":["b.com"],"backend":"static","unknown":1}`,
	}
	for i, n := range negatives {
		if err := ValidateWebSiteSpec(json.RawMessage(n)); err == nil {
			t.Errorf("neg[%d] KABUL EDİLDİ: %s", i, n)
		}
	}
	// 8 KiB payload sınırı.
	big := `{"name":"ok","server_names":["` + strings.Repeat("a", 8<<10) + `"],"backend":"static"}`
	if err := ValidateWebSiteSpec(json.RawMessage(big)); err == nil {
		t.Error("8 KiB üstü payload reddedilmeli")
	}
}

func TestValidateWebPHPList(t *testing.T) {
	if err := ValidateWebPHPList(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidateWebPHPList(json.RawMessage(`{}`)); err != nil {
		t.Errorf("boş obje geçerli: %v", err)
	}
	if err := ValidateWebPHPList(json.RawMessage(`{"x":1}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

// TestFaz08RegistrySpecs: RBAC eşleme + sınıf disiplini (kriter 1 + RBAC).
func TestFaz08RegistrySpecs(t *testing.T) {
	reg, err := BuildDefaultRegistry(&fakeTagSetter{}, Faz09Services{}, Faz14Services{}, Faz15Services{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*OperationSpec{}
	for _, n := range reg.Names() {
		spec, _ := reg.Spec(n)
		byName[n] = spec
	}
	type want struct {
		perm  string
		class Class
		safe  bool
	}
	cases := map[string]want{
		"web.site.list":    {rbac.PermWebRead, ClassReadOnly, true},
		"web.site.status":  {rbac.PermWebRead, ClassReadOnly, true},
		"web.php.list":     {rbac.PermWebRead, ClassReadOnly, true},
		"web.site.create":  {rbac.PermWebControl, ClassIdempotent, true},
		"web.site.update":  {rbac.PermWebControl, ClassIdempotent, true},
		"web.site.disable": {rbac.PermWebControl, ClassIdempotent, true},
		"web.site.delete":  {rbac.PermWebControl, ClassNonIdem, false},
	}
	for name, w := range cases {
		spec, ok := byName[name]
		if !ok {
			t.Fatalf("%s registry'de yok", name)
		}
		if spec.RequiredPerm != w.perm {
			t.Errorf("%s perm = %s, %s beklenir", name, spec.RequiredPerm, w.perm)
		}
		if spec.Class != w.class {
			t.Errorf("%s class = %v, %v beklenir", name, spec.Class, w.class)
		}
		if spec.RetrySafe != w.safe {
			t.Errorf("%s retrySafe = %v, %v beklenir", name, spec.RetrySafe, w.safe)
		}
		if spec.Target != TargetNode {
			t.Errorf("%s target node olmalı", name)
		}
		if spec.Timeout <= 0 || spec.Timeout > 10*time.Minute {
			t.Errorf("%s timeout sınırsız: %v", name, spec.Timeout)
		}
	}
}
