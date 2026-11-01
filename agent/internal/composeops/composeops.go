// Package composeops implements the Faz 07 Compose analysis surface. It is
// PURE: no Docker daemon access, no host access, no execution — parse,
// validate and report (typed findings, bounded input).
//
// Faz 07 scope (ADR-0011):
//   - docker.compose.validate: schema sanity + policy risk scan (ports,
//     mounts, privileged, capabilities) BEFORE any deploy attempt
//     (criterion: pre-deploy validation must surface risks);
//   - docker.compose.deploy: validation + a deployment PLAN as data —
//     the deploy itself is NOT executed in Faz 07 (Executed=false
//     invariant; execution needs an approval flow, future ADR).
//
// Secret hygiene: findings name environment KEYS only — values are never
// copied into the report, logs or audit (agents.md §4).
package composeops

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Operation names (mirror backend/internal/tasks ops_faz07.go).
const (
	OpDockerComposeValidate = "docker.compose.validate"
	OpDockerComposeDeploy   = "docker.compose.deploy" // PREVIEW/PLAN-ONLY
)

// Input/output bounds.
const (
	MaxComposeBytes = 64 << 10
	MaxServices     = 64
	MaxFindings     = 100
)

// Stable errors.
var (
	ErrInvalidCompose = errors.New("geçersiz compose girdisi")
	ErrTooLarge       = errors.New("compose içeriği çok büyük")
)

// Severity values (closed set).
const (
	SevHigh   = "high"
	SevMedium = "medium"
	SevLow    = "low"
)

// Finding is one policy risk (typed; rule ids are stable API).
type Finding struct {
	Severity string `json:"severity"` // high|medium|low
	Rule     string `json:"rule"`     // e.g. privileged, docker-sock-mount
	Service  string `json:"service,omitempty"`
	Message  string `json:"message"`
}

// ValidateReport is the docker.compose.validate result.
type ValidateReport struct {
	Valid     bool      `json:"valid"` // YAML parsed + minimal schema holds
	Errors    []string  `json:"errors,omitempty"`
	Findings  []Finding `json:"findings,omitempty"`
	Services  int       `json:"services"`
	Truncated bool      `json:"truncated,omitempty"` // findings cap hit
}

// DeployPlan is the docker.compose.deploy result: validation + the ordered
// action list as DATA. Executed is ALWAYS false (ADR-0011) — this adapter
// has no deploy execution path at all.
type DeployPlan struct {
	ValidateReport
	Actions  []string `json:"actions,omitempty"`
	Executed bool     `json:"executed"`
	Note     string   `json:"note"`
}

// ComposePayload is the typed envelope (validated on BOTH sides).
type ComposePayload struct {
	Compose string `json:"compose"`
}

// ValidatePayload enforces the envelope bounds (size + non-empty).
func ValidatePayload(p ComposePayload) error {
	if strings.TrimSpace(p.Compose) == "" {
		return fmt.Errorf("%w: compose boş", ErrInvalidCompose)
	}
	if len(p.Compose) > MaxComposeBytes {
		return fmt.Errorf("%w: %d > %d bayt", ErrTooLarge, len(p.Compose), MaxComposeBytes)
	}
	if strings.ContainsRune(p.Compose, 0) {
		return fmt.Errorf("%w: NUL baytı reddedilir", ErrInvalidCompose)
	}
	return nil
}

// ---- compose şablonu (yalnız analiz ettiğimiz alanlar) ----

type stack struct {
	Services map[string]serviceDef `yaml:"services"`
	Networks map[string]any        `yaml:"networks"`
	Volumes  map[string]any        `yaml:"volumes"`
}

type serviceDef struct {
	Image       string        `yaml:"image"`
	Build       any           `yaml:"build"` // varlığı bilinir; derleme analizi kapsam dışı
	Privileged  bool          `yaml:"privileged"`
	CapAdd      []string      `yaml:"cap_add"`
	NetworkMode string        `yaml:"network_mode"`
	Pid         string        `yaml:"pid"`
	Ipc         string        `yaml:"ipc"`
	User        string        `yaml:"user"`
	ReadOnly    bool          `yaml:"read_only"`
	Restart     string        `yaml:"restart"`
	Ports       []portEntry   `yaml:"ports"`
	Volumes     []volumeEntry `yaml:"volumes"`
	Environment yaml.Node     `yaml:"environment"` // liste veya map — değerler ASLA kopyalanmaz
	EnvFile     envFiles      `yaml:"env_file"`
	Devices     []string      `yaml:"devices"`
	DependsOn   []string      `yaml:"depends_on"`
}

// portEntry kabul eder: "8080:80", "127.0.0.1:8080:80", "80/udp" ve map biçimi.
type portEntry struct {
	raw                            string
	pubIP, pubPort, tgtPort, proto string
}

// UnmarshalYAML string ve map biçimlerini ayrıştırır.
func (p *portEntry) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		p.raw = s
		parseShortPort(p, s)
		return nil
	}
	var m struct {
		Published string `yaml:"published"`
		Target    int    `yaml:"target"`
		Mode      string `yaml:"mode"`
		HostIP    string `yaml:"host_ip"`
		Protocol  string `yaml:"protocol"`
	}
	if err := unmarshal(&m); err != nil {
		return fmt.Errorf("ports girdisi anlaşılamadı")
	}
	p.pubIP, p.pubPort, p.tgtPort = m.HostIP, m.Published, itoa(m.Target)
	p.proto = m.Protocol
	return nil
}

func parseShortPort(p *portEntry, s string) {
	rest := s
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		p.proto = rest[i+1:]
		rest = rest[:i]
	}
	parts := strings.Split(rest, ":")
	switch len(parts) {
	case 1:
		p.tgtPort = parts[0]
	case 2:
		p.pubPort, p.tgtPort = parts[0], parts[1]
	default:
		p.pubIP, p.pubPort, p.tgtPort = parts[0], parts[1], parts[2]
	}
}

// volumeEntry kabul eder: "isim:/yol", "./göreli:/yol", "/etc/x:/etc/x:ro" ve map biçimi.
type volumeEntry struct{ raw, srcType, source, target string }

func (v *volumeEntry) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		v.raw = s
		parseShortVolume(v, s)
		return nil
	}
	var m struct {
		Type   string `yaml:"type"`
		Source string `yaml:"source"`
		Target string `yaml:"target"`
	}
	if err := unmarshal(&m); err != nil {
		return fmt.Errorf("volumes girdisi anlaşılamadı")
	}
	v.srcType, v.source, v.target = m.Type, m.Source, m.Target
	return nil
}

func parseShortVolume(v *volumeEntry, s string) {
	parts := strings.SplitN(s, ":", 3)
	switch len(parts) {
	case 1:
		v.srcType, v.target = "volume", parts[0]
	case 2:
		v.srcType, v.source, v.target = classifySrc(parts[0]), parts[0], parts[1]
	default:
		v.srcType, v.source, v.target = classifySrc(parts[0]), parts[0], parts[1]
	}
}

func classifySrc(s string) string {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return "bind"
	}
	return "volume"
}

// envFiles hem string hem liste biçimini kabul eder.
type envFiles []string

func (e *envFiles) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		*e = []string{s}
		return nil
	}
	var l []string
	if err := unmarshal(&l); err != nil {
		return fmt.Errorf("env_file biçimi anlaşılamadı")
	}
	*e = l
	return nil
}

// envKeys environment'un İKİ biçiminden de yalnız ANAHTAR adlarını çıkarır
// ("KEY=value" satırı veya KEY: value map'i). Değerler hiçbir yere yazılmaz.
func (s *serviceDef) envKeys() []string {
	if s.Environment.Kind == 0 { // environment yok
		return nil
	}
	var keys []string
	switch s.Environment.Kind {
	case yaml.SequenceNode:
		for _, n := range s.Environment.Content {
			if n.Kind != yaml.ScalarNode {
				continue
			}
			key := n.Value
			if i := strings.Index(n.Value, "="); i >= 0 {
				key = n.Value[:i]
			}
			keys = append(keys, key)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(s.Environment.Content); i += 2 {
			keys = append(keys, s.Environment.Content[i].Value)
		}
	}
	return keys
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

var secretKeyRE = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|apikey|private[_-]?key|credential)`)

var capDangerous = map[string]bool{
	"ALL": true, "SYS_ADMIN": true, "NET_ADMIN": true, "SYS_PTRACE": true,
}

var sensitiveBinds = map[string]bool{
	"/": true, "/etc": true, "/proc": true, "/sys": true, "/boot": true, "/dev": true,
}

// serviceNameRE: compose service adları [A-Za-z0-9._-]+.
var serviceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Validate parses and analyzes one compose file. Pure; deterministic
// (services sorted; findings sorted by severity → rule → service).
func Validate(src string) ValidateReport {
	rep := ValidateReport{}
	if err := ValidatePayload(ComposePayload{Compose: src}); err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep
	}
	var st stack
	dec := yaml.NewDecoder(strings.NewReader(src))
	if err := dec.Decode(&st); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("yaml ayrıştırma hatası: %v", err))
		return rep
	}
	if len(st.Services) == 0 {
		rep.Errors = append(rep.Errors, "services bölümü boş veya yok")
		return rep
	}
	if len(st.Services) > MaxServices {
		rep.Errors = append(rep.Errors, fmt.Sprintf("services sayısı %d > %d sınırı", len(st.Services), MaxServices))
		return rep
	}
	rep.Valid = true
	rep.Services = len(st.Services)

	names := make([]string, 0, len(st.Services))
	for n := range st.Services {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		if !serviceNameRE.MatchString(name) {
			rep.Valid = false
			rep.Errors = append(rep.Errors, fmt.Sprintf("geçersiz servis adı: %q", name))
			continue
		}
		svc := st.Services[name]
		if svc.Image == "" && svc.Build == nil {
			rep.Valid = false
			rep.Errors = append(rep.Errors, fmt.Sprintf("servis %q: image veya build gerekli", name))
		}
		scanService(&rep, name, &svc)
	}
	// depends_on tutarlılığı (deploy planı için de aynı kontrol).
	if err := checkDeps(st); err != nil {
		rep.Valid = false
		rep.Errors = append(rep.Errors, err.Error())
	}
	sortFindings(rep.Findings)
	if len(rep.Findings) > MaxFindings {
		rep.Findings = rep.Findings[:MaxFindings]
		rep.Truncated = true
	}
	return rep
}

// BuildDeployPlan validates and returns the ordered deployment plan as DATA.
// Nothing is executed (Executed=false invariant, ADR-0011).
func BuildDeployPlan(src string) DeployPlan {
	rep := Validate(src)
	plan := DeployPlan{ValidateReport: rep, Executed: false,
		Note: "PLAN — hiçbir kaynak oluşturulmadı. Compose deploy yürütmesi operatör onaylı ayrı fazda tasarlanacaktır (ADR-0011)."}
	if !rep.Valid {
		return plan
	}
	var st stack
	_ = yaml.Unmarshal([]byte(src), &st) // Validate zaten ayrıştırdı
	for _, n := range sortedKeys(st.Networks) {
		plan.Actions = append(plan.Actions, "network oluştur: "+n)
	}
	for _, n := range sortedKeys(st.Volumes) {
		plan.Actions = append(plan.Actions, "volume oluştur: "+n)
	}
	order, ok := topoOrder(st.Services)
	if !ok {
		return plan // döngü zaten Valid=false yapmış olmalı; savunma amaçlı
	}
	for _, name := range order {
		svc := st.Services[name]
		img := svc.Image
		if img == "" {
			img = "(build)"
		}
		plan.Actions = append(plan.Actions, "container oluştur: "+name+" (image="+img+")")
	}
	for _, name := range order {
		plan.Actions = append(plan.Actions, "container başlat: "+name)
	}
	return plan
}

func scanService(rep *ValidateReport, name string, svc *serviceDef) {
	add := func(sev, rule, msg string) {
		rep.Findings = append(rep.Findings, Finding{Severity: sev, Rule: rule, Service: name, Message: msg})
	}
	if svc.Privileged {
		add(SevHigh, "privileged", "privileged mod — host çekirdeğine tam erişim riski")
	}
	for _, c := range svc.CapAdd {
		if capDangerous[c] {
			add(SevHigh, "cap-add-dangerous", "cap_add tehlikeli yetenek: "+c)
		} else {
			add(SevMedium, "cap-add", "cap_add ek yetenek: "+c)
		}
	}
	if svc.NetworkMode == "host" {
		add(SevHigh, "network-mode-host", "network_mode: host — host ağ yığını paylaşılıyor")
	} else if strings.HasPrefix(svc.NetworkMode, "container:") {
		add(SevMedium, "network-mode-container", "başka container'ın ağ ad alanı paylaşılıyor")
	}
	if svc.Pid == "host" {
		add(SevHigh, "pid-host", "pid: host — host süreç tablosu görünür")
	}
	if svc.Ipc == "host" {
		add(SevMedium, "ipc-host", "ipc: host — host IPC ad alanı paylaşılıyor")
	}
	if u := svc.User; u == "" || u == "root" || u == "0" {
		add(SevMedium, "root-user", "container root olarak çalışır (user belirtilmemiş/0)")
	}
	if !svc.ReadOnly {
		add(SevLow, "writable-rootfs", "kök dosya sistemi yazılabilir (read_only ayarlanmamış)")
	}
	if svc.Restart == "" {
		add(SevLow, "no-restart-policy", "restart politikası yok — host yeniden başladığında servis kalkmaz")
	}
	if len(svc.Devices) > 0 {
		add(SevMedium, "devices", "host aygıtları container'a geçiriliyor")
	}
	for _, p := range svc.Ports {
		hostIP := p.pubIP
		if hostIP == "" || hostIP == "0.0.0.0" {
			add(SevMedium, "public-port", "port tüm arayüzlerde açılır: "+portDisplay(p))
		}
		if n, ok := atoi(p.pubPort); ok && n > 0 && n < 1024 {
			add(SevLow, "privileged-port", "ayrıcalıklı host portu (<1024): "+p.pubPort)
		}
	}
	for _, v := range svc.Volumes {
		if v.srcType == "bind" || strings.HasPrefix(v.source, "/") {
			if strings.Contains(v.source, "docker.sock") {
				add(SevHigh, "docker-sock-mount", "docker.sock bağlandı — host üzerinde root eşdeğeri erişim: "+v.source)
				continue
			}
			if sensitiveBinds[strings.TrimSuffix(v.source, "/")] {
				add(SevHigh, "sensitive-bind", "hassas host dizini bağlandı: "+v.source)
				continue
			}
			add(SevLow, "bind-mount", "host dizini bağlandı: "+v.source+" → "+v.target)
		}
	}
	for _, k := range svc.envKeys() {
		if secretKeyRE.MatchString(k) {
			add(SevMedium, "env-secret-name", "gizli bilgi olabilecek ortam anahtarı (değer taranmaz/yazılmaz): "+k)
		}
	}
	if len(svc.EnvFile) > 0 {
		add(SevLow, "env-file", "env_file içeriği bu analizde taranamaz")
	}
}

// checkDeps: depends_on bilinmeyen servis veya döngü → hata.
func checkDeps(st stack) error {
	for name, svc := range st.Services {
		for _, d := range svc.DependsOn {
			if _, ok := st.Services[d]; !ok {
				return fmt.Errorf("servis %q: depends_on bilinmeyen servis %q", name, d)
			}
		}
	}
	if _, ok := topoOrder(st.Services); !ok {
		return fmt.Errorf("depends_on döngüsü saptandı")
	}
	return nil
}

// topoOrder: depends_on'a göre deterministik sıralama; döngüde ok=false.
func topoOrder(services map[string]serviceDef) ([]string, bool) {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	state := make(map[string]int, len(names)) // 0 yeni, 1 yolda, 2 bitti
	var out []string
	var visit func(string) bool
	visit = func(n string) bool {
		switch state[n] {
		case 1:
			return false // döngü
		case 2:
			return true
		}
		state[n] = 1
		for _, d := range services[n].DependsOn {
			if !visit(d) {
				return false
			}
		}
		state[n] = 2
		out = append(out, n)
		return true
	}
	for _, n := range names {
		if !visit(n) {
			return nil, false
		}
	}
	return out, true
}

func sortFindings(fs []Finding) {
	sevRank := map[string]int{SevHigh: 0, SevMedium: 1, SevLow: 2}
	sort.SliceStable(fs, func(i, j int) bool {
		if sevRank[fs[i].Severity] != sevRank[fs[j].Severity] {
			return sevRank[fs[i].Severity] < sevRank[fs[j].Severity]
		}
		if fs[i].Rule != fs[j].Rule {
			return fs[i].Rule < fs[j].Rule
		}
		return fs[i].Service < fs[j].Service
	})
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func portDisplay(p portEntry) string {
	if p.pubPort != "" {
		return p.pubPort + ":" + p.tgtPort
	}
	return p.tgtPort
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
		if n > 65535 {
			return 0, false
		}
	}
	return n, true
}
