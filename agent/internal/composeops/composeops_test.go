package composeops

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodStack = `services:
  web:
    image: nginx:1.25
    ports:
      - "127.0.0.1:8080:80"
    volumes:
      - app-data:/var/www
    restart: unless-stopped
    user: "101"
    read_only: true
volumes:
  app-data:
`

func TestValidateGoodStack(t *testing.T) {
	rep := Validate(goodStack)
	if !rep.Valid {
		t.Fatalf("geçerli stack reddedildi: %+v", rep.Errors)
	}
	if rep.Services != 1 {
		t.Fatalf("services: %d", rep.Services)
	}
	// Temiz stack: kök-bağlama/privilege yok. Sadece bilgi düzeyi bulgu bile
	// olmamalı (user=101, read_only, restart var, port localhost'a bağlı).
	for _, f := range rep.Findings {
		if f.Severity == SevHigh || f.Severity == SevMedium {
			t.Errorf("temiz stack'te yüksek/orta bulgu: %+v", f)
		}
	}
}

func TestValidateSchemaErrors(t *testing.T) {
	cases := []struct {
		name, src string
		want      string
	}{
		{"boş", "", "compose boş"},
		{"services yok", "version: '3'\nnetworks: {}\n", "services"},
		{"image yok", "services:\n  web:\n    ports: ['80:80']\n", "image veya build"},
		{"yaml hatası", "services:\n  web:\n   image: [bozuk\n", "yaml"},
		{"geçersiz servis adı", "services:\n  'web/x':\n    image: nginx\n", "geçersiz servis adı"},
		{"depends_on bilinmeyen", "services:\n  a:\n    image: x\n    depends_on: [b]\n", "bilinmeyen servis"},
		{"döngü", "services:\n  a:\n    image: x\n    depends_on: [b]\n  b:\n    image: y\n    depends_on: [a]\n", "döngü"},
	}
	for _, c := range cases {
		rep := Validate(c.src)
		if rep.Valid {
			t.Errorf("%s: Valid=true olmamalı", c.name)
			continue
		}
		joined := strings.Join(rep.Errors, " | ")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: hata beklentisi %q yok: %s", c.name, c.want, joined)
		}
	}
}

func TestValidateDangerousPolicies(t *testing.T) {
	src := `services:
  evil:
    image: hack:latest
    privileged: true
    cap_add: [SYS_ADMIN, NET_RAW]
    network_mode: host
    pid: "host"
    ipc: "host"
    devices: ["/dev/mem:/dev/mem"]
    ports:
      - "443:443"
      - "0.0.0.0:8080:80"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /etc:/etc
      - ./app:/srv/app
    environment:
      - DB_PASSWORD=supergizli-deger-yok-sanal
      - API_TOKEN=abc
    env_file: .env
`
	rep := Validate(src)
	if !rep.Valid {
		t.Fatalf("şema geçerli olmalı: %+v", rep.Errors)
	}
	have := map[string]bool{}
	for _, f := range rep.Findings {
		have[f.Rule] = true
		if f.Service != "evil" {
			t.Errorf("bulgu servisi: %+v", f)
		}
	}
	for _, want := range []string{
		"privileged", "cap-add-dangerous", "cap-add", "network-mode-host",
		"pid-host", "ipc-host", "devices", "public-port", "privileged-port",
		"docker-sock-mount", "sensitive-bind", "bind-mount",
		"env-secret-name", "env-file",
	} {
		if !have[want] {
			t.Errorf("kural eksik: %s (bulunan: %v)", want, keysOf(have))
		}
	}
	// Secret DEĞERLERİ asla rapora sızmasın (yalnız anahtar adları).
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "supergizli") || strings.Contains(string(b), "abc\"") {
		t.Fatalf("gizli değer rapora sızdı: %s", b)
	}
	if !strings.Contains(string(b), "DB_PASSWORD") {
		t.Fatalf("anahtar adı raporda olmalı")
	}
	// Ağırlık sırası: ilk bulgu high olmalı.
	if rep.Findings[0].Severity != SevHigh {
		t.Fatalf("ilk bulgu high olmalı: %+v", rep.Findings[0])
	}
}

func TestValidateMapForms(t *testing.T) {
	src := `services:
  api:
    image: api:1
    ports:
      - target: 80
        published: "9000"
        host_ip: "0.0.0.0"
    volumes:
      - type: bind
        source: /var/run/docker.sock
        target: /var/run/docker.sock
    environment:
      SECRET_KEY: value-kopyalanmamali
`
	rep := Validate(src)
	if !rep.Valid {
		t.Fatalf("map biçimi ayrıştırılamadı: %+v", rep.Errors)
	}
	have := map[string]bool{}
	for _, f := range rep.Findings {
		have[f.Rule] = true
	}
	if !have["public-port"] || !have["docker-sock-mount"] || !have["env-secret-name"] {
		t.Fatalf("map biçimi kuralları: %v", keysOf(have))
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "value-kopyalanmamali") {
		t.Fatalf("map değeri sızdı")
	}
}

func TestValidateBounds(t *testing.T) {
	if err := ValidatePayload(ComposePayload{Compose: strings.Repeat("a", MaxComposeBytes+1)}); err == nil {
		t.Fatal("boyut sınırı çalışmalı")
	}
	if err := ValidatePayload(ComposePayload{Compose: "services: {}"}); err != nil {
		t.Fatalf("küçük girdi kabul: %v", err)
	}
	// NUL reddi
	if err := ValidatePayload(ComposePayload{Compose: "services:\x00"}); err == nil {
		t.Fatal("NUL reddedilmeli")
	}
	// findings cap: 64 servisten fazla olan tek stack yerine cap'i küçük bir
	// üretimle sına: 64 servis × writable-rootfs düşük bulgular cap'i zorlar.
	var b strings.Builder
	b.WriteString("services:\n")
	for i := 0; i < MaxServices; i++ {
		b.WriteString("  svc" + itoa(i) + ":\n    image: x\n")
	}
	rep := Validate(b.String())
	if !rep.Valid || !rep.Truncated {
		t.Fatalf("cap beklenir: valid=%v truncated=%v findings=%d", rep.Valid, rep.Truncated, len(rep.Findings))
	}
	if len(rep.Findings) != MaxFindings {
		t.Fatalf("findings: %d, beklenen %d", len(rep.Findings), MaxFindings)
	}
}

func TestDeployPlanInvariants(t *testing.T) {
	plan := BuildDeployPlan(goodStack)
	if plan.Executed {
		t.Fatal("deploy planı Executed=true olamaz (ADR-0011)")
	}
	if plan.Note == "" {
		t.Fatal("plan notu boş olmamalı")
	}
	want := []string{
		"volume oluştur: app-data",
		"container oluştur: web (image=nginx:1.25)",
		"container başlat: web",
	}
	if len(plan.Actions) != len(want) {
		t.Fatalf("aksiyonlar: %v", plan.Actions)
	}
	for i, w := range want {
		if plan.Actions[i] != w {
			t.Fatalf("aksiyon %d: %q ≠ %q", i, plan.Actions[i], w)
		}
	}
	bad := BuildDeployPlan("services:\n  a:\n    image: x\n    depends_on: [b]\n")
	if bad.Executed || len(bad.Actions) != 0 || bad.Valid {
		t.Fatalf("geçersiz stack'te boş plan beklenir: %+v", bad)
	}
}

func TestDeployPlanDependencyOrder(t *testing.T) {
	src := `services:
  worker:
    image: w:1
    depends_on: [db]
  db:
    image: postgres:16
`
	plan := BuildDeployPlan(src)
	var createW, createDB int
	for i, a := range plan.Actions {
		if strings.HasPrefix(a, "container oluştur: worker") {
			createW = i
		}
		if strings.HasPrefix(a, "container oluştur: db") {
			createDB = i
		}
	}
	if createDB > createW {
		t.Fatalf("depends_on sırası bozuldu: db(%d) > worker(%d)", createDB, createW)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
