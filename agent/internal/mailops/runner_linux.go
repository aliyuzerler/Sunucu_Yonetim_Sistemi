//go:build linux

// Runner implementation for the Faz 11 mail adapter (Postfix + Dovecot +
// Rspamd). EVERY mutation follows the invariant of ADR-0015 (criterion 4):
//
//	backup (in memory) → write new content (atomic rename) → compile maps
//	(postmap) → SYNTAX CHECK (postfix check / doveconf -n / doveadm user)
//	  ↳ failure → restore backup → recompile → typed ErrSyntaxCheck
//	  ↳ success → best-effort reload (reloaded flag reported honestly)
//
// All external programs run with fixed argv (no shell, no interpolation of
// anything beyond allowlist-validated identifiers). Map/user files live in
// the managed directory; main.cf and conf.d are touched ONLY through
// marker-delimited managed blocks/fragments.
package mailops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Default mailbox quota applied at create time (quota later via
// mail.quota.set). 1 GiB is the documented default.
const DefaultQuotaBytes = int64(1) << 30

// Runner executes mail operations on the node. Tool paths and directories
// are injectable for hermetic tests; the zero value looks binaries up on
// PATH and uses the default managed paths.
type Runner struct {
	Postfix   string // postfix ("" → LookPath)
	Postmap   string // postmap
	Postqueue string // postqueue
	Postconf  string // postconf
	Doveconf  string // doveconf
	Doveadm   string // doveadm
	Rspamadm  string // rspamadm (optional)
	Systemctl string // systemctl (optional; status probes)

	BaseDir        string // managed config dir (default /etc/server-control/mail)
	PostfixMainCF  string // default /etc/postfix/main.cf
	DovecotConfDir string // default /etc/dovecot/conf.d
	VMailBase      string // default /var/vmail

	Timeout time.Duration

	// vmailLookup is overridable for tests (default: os/user lookup of the
	// documented "vmail" system user).
	vmailLookup func() (uid, gid uint32, err error)
	// chownFn is overridable for tests (default os.Chown; the sandbox test
	// user cannot chown to vmail, production broker runs as root).
	chownFn func(path string, uid, gid int) error
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 120 * time.Second
}

func (r *Runner) baseDir() string {
	if r.BaseDir != "" {
		return r.BaseDir
	}
	return DefaultBaseDir
}

func (r *Runner) mainCF() string {
	if r.PostfixMainCF != "" {
		return r.PostfixMainCF
	}
	return DefaultPostfixMainCF
}

func (r *Runner) confDir() string {
	if r.DovecotConfDir != "" {
		return r.DovecotConfDir
	}
	return DefaultDovecotConfDir
}

func (r *Runner) vmailBase() string {
	if r.VMailBase != "" {
		return r.VMailBase
	}
	return DefaultVMailBase
}

// resolve looks a tool up (injectable path wins).
func resolve(injected, name string) (string, bool) {
	if injected != "" {
		return injected, true
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// runArgv executes one fixed program with an argument vector (no shell).
// stderr detail is intentionally NOT returned (log safety); errors carry a
// short program context only.
func runArgv(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + argv, no user shell
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%s başarısız: %w", filepath.Base(bin), err)
	}
	return stdout.Bytes(), nil
}

// lookup resolves a Runner binary or fails with ErrUnavailable.
func (r *Runner) lookup(injected, name string) (string, error) {
	if p, ok := resolve(injected, name); ok {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s bulunamadı", ErrUnavailable, name)
}

// ---- typed results (JSON contracts mirrored by the control plane) ----

// BinStatus is one binary probe of preflight.
type BinStatus struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Version string `json:"version,omitempty"`
}

// PortStatus is one port probe of preflight (criterion 1).
type PortStatus struct {
	Port     int    `json:"port"`
	Occupied bool   `json:"occupied"`
	State    string `json:"state"` // free|occupied|unknown
}

// PTRStatus is the best-effort reverse-DNS probe. "unknown" is an honest
// state: the authoritative record lives in EXTERNAL DNS (criterion 3).
type PTRStatus struct {
	IP     string   `json:"ip,omitempty"`
	Names  []string `json:"names,omitempty"`
	State  string   `json:"state"` // resolved|unknown|no_ip
	Detail string   `json:"detail,omitempty"`
}

// PreflightResult reports installation/port/wiring state WITHOUT changing
// anything (criterion 1: the module is never mandatory and installs nothing).
type PreflightResult struct {
	PostfixInstalled bool         `json:"postfix_installed"`
	DovecotInstalled bool         `json:"dovecot_installed"`
	RspamdInstalled  bool         `json:"rspamd_installed"`
	Binaries         []BinStatus  `json:"binaries"`
	Ports            []PortStatus `json:"ports"`
	VmailUser        bool         `json:"vmail_user"`
	ManagedDirReady  bool         `json:"managed_dir_ready"`
	PostfixWired     bool         `json:"postfix_wired"`
	DovecotWired     bool         `json:"dovecot_wired"`
	Hostname         string       `json:"hostname"`
	Ptr              PTRStatus    `json:"ptr"`
	Notes            []string     `json:"notes,omitempty"`
}

// StatusResult is the mail.status summary.
type StatusResult struct {
	Services     map[string]string `json:"services"` // postfix|dovecot|rspamd → active|inactive|unknown
	Queue        QueueSummary      `json:"queue"`
	Domains      int               `json:"domains"`
	Mailboxes    int               `json:"mailboxes"`
	Aliases      int               `json:"aliases"`
	PostfixWired bool              `json:"postfix_wired"`
	DovecotWired bool              `json:"dovecot_wired"`
}

// QueueSummary is the aggregate queue view (no content).
type QueueSummary struct {
	Total     int    `json:"total"`
	Bytes     int64  `json:"bytes"`
	Deferred  int    `json:"deferred"`
	ProbeMode string `json:"probe_mode"` // postqueue|unavailable
}

// DomainInfo is one managed virtual domain.
type DomainInfo struct {
	Name string `json:"name"`
}

// MailboxInfo is one managed mailbox (NO password material).
type MailboxInfo struct {
	Address    string `json:"address"`
	Maildir    string `json:"maildir"`
	QuotaBytes int64  `json:"quota_bytes"`
}

// AliasInfo is one managed alias mapping.
type AliasInfo struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
}

// QueueEntry is ONE queue item. Deliberately minimal: queue id, size, age,
// sender and the (truncated) deferral reason. Message bodies and recipient
// addresses NEVER appear here (criterion 5).
type QueueEntry struct {
	QueueID    string `json:"queue_id"`
	QueueName  string `json:"queue_name"`
	SizeBytes  int64  `json:"size_bytes"`
	AgeSeconds int64  `json:"age_seconds"`
	Sender     string `json:"sender,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ConfigCheckResult carries the three syntax probes (criterion 4 read-only
// view; mutations run the same gates BEFORE applying).
type ConfigCheckResult struct {
	Postfix CheckOutcome `json:"postfix"`
	Dovecot CheckOutcome `json:"dovecot"`
	Rspamd  CheckOutcome `json:"rspamd"`
}

// CheckOutcome is one component's syntax check outcome.
type CheckOutcome struct {
	State  string `json:"state"` // pass|fail|absent
	Detail string `json:"detail,omitempty"`
}

// LimitsGetResult is the current managed-tunable snapshot.
type LimitsGetResult struct {
	Limits map[string]string `json:"limits"`
}

// LimitsSetResult reports an honest apply: reloaded=false means the config
// is VALID and stored but the daemon was not running to pick it up live.
type LimitsSetResult struct {
	Applied  bool     `json:"applied"`
	Reloaded bool     `json:"reloaded"`
	Keys     []string `json:"keys"`
}

// MutateResult is the common mutation outcome (create/delete domain,
// mailbox, alias; quota set): reloaded=false is honest, not an error.
type MutateResult struct {
	Applied  bool `json:"applied"`
	Reloaded bool `json:"reloaded"`
}

// ---- preflight (criterion 1) ----

// Preflight probes binaries, ports, users and wiring. It CHANGES NOTHING.
func (r *Runner) Preflight(ctx context.Context) (PreflightResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	res := PreflightResult{
		Hostname: hostName(),
		Ptr:      PTRStatus{State: "unknown"},
	}

	type probe struct {
		name     string
		injected string
	}
	core := map[string]bool{}
	for _, p := range []probe{
		{"postfix", r.Postfix}, {"postmap", r.Postmap}, {"postqueue", r.Postqueue},
		{"postconf", r.Postconf}, {"doveconf", r.Doveconf}, {"doveadm", r.Doveadm},
		{"rspamadm", r.Rspamadm},
	} {
		bs := BinStatus{Name: p.name}
		if bin, ok := resolve(p.injected, p.name); ok {
			bs.Found = true
			bs.Version = r.probeVersion(ctx, p.name, bin)
		}
		core[p.name] = bs.Found
		res.Binaries = append(res.Binaries, bs)
	}
	// The postfix toolchain is "installed" only when ALL core binaries are
	// present (check/map/queue/conf are each required by mutations).
	res.PostfixInstalled = core["postfix"] && core["postmap"] && core["postqueue"] && core["postconf"]
	res.DovecotInstalled = core["doveconf"] && core["doveadm"]
	res.RspamdInstalled = core["rspamadm"]

	for _, port := range PreflightPorts {
		res.Ports = append(res.Ports, probePort(port))
	}

	if uid, gid, err := r.vmail(); err == nil {
		_, _ = uid, gid
		res.VmailUser = true
	}
	if _, err := os.Stat(r.baseDir()); err == nil {
		res.ManagedDirReady = true
	}
	if mainBytes, err := os.ReadFile(r.mainCF()); err == nil { //nolint:gosec // operator-managed path constant
		res.PostfixWired = bytes.Contains(mainBytes, []byte(MainCFBegin)) &&
			bytes.Contains(mainBytes, []byte(MainCFEnd))
	}
	confPath := filepath.Join(r.confDir(), DovecotConfName)
	if confBytes, err := os.ReadFile(confPath); err == nil { //nolint:gosec // managed fragment path
		res.DovecotWired = bytes.Contains(confBytes, []byte(MainCFBegin))
	}

	res.Ptr = probePTR()
	if !res.PostfixInstalled {
		res.Notes = append(res.Notes, "postfix kurulu değil — modül kurulum YAPMAZ; paketi operatör kurar (kriter 1)")
	}
	if !res.DovecotInstalled {
		res.Notes = append(res.Notes, "dovecot kurulu değil — IMAP/POP3 çalışmaz")
	}
	if res.PostfixInstalled && !res.VmailUser {
		res.Notes = append(res.Notes, "vmail sistemi kullanıcısı yok — mailbox işlemleri reddedilir")
	}
	if res.Ptr.State != "resolved" {
		res.Notes = append(res.Notes, "PTR/reverse DNS dış DNS bağımlılığıdır — panel buradan doğrulayamaz (UI'daki deliverability rehberine bakın)")
	}
	return res, nil
}

func (r *Runner) probeVersion(ctx context.Context, name, bin string) string {
	var args []string
	switch name {
	case "postfix":
		return "" // postfix has no --version; postconf reports mail_version
	case "postmap", "postqueue", "postconf":
		return "" // version probing is noise for these
	case "rspamadm":
		args = []string{"--version"}
	default:
		args = []string{"--version"}
	}
	vctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runArgv(vctx, bin, args...)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// probePort answers free|occupied|unknown. A bind attempt is the honest
// kernel-level occupancy test; other errors are reported as unknown.
func probePort(port int) PortStatus {
	st := PortStatus{Port: port, State: "unknown"}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err == nil {
		_ = ln.Close()
		st.State = "free"
		return st
	}
	if isAddrInUse(err) {
		st.Occupied = true
		st.State = "occupied"
	}
	return st
}

func isAddrInUse(err error) bool {
	var oe *net.OpError
	if errors.As(err, &oe) {
		var se syscall.Errno
		if errors.As(oe.Err, &se) {
			return se == syscall.EADDRINUSE
		}
	}
	return false
}

// probePTR is best-effort: pick the first global non-loopback address and
// ask resolver for PTR. ANY failure degrades to "unknown" (never blocks,
// never claims deliverability).
func probePTR() PTRStatus {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return PTRStatus{State: "no_ip"}
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || !ipnet.IP.IsGlobalUnicast() {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil {
			continue
		}
		pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		names, lerr := net.DefaultResolver.LookupAddr(pctx, ip.String())
		cancel()
		st := PTRStatus{IP: ip.String(), State: "unknown"}
		if lerr == nil && len(names) > 0 {
			for i := range names {
				names[i] = strings.TrimSuffix(names[i], ".")
				if len(names[i]) > 253 {
					names[i] = names[i][:253]
				}
			}
			st.Names = names
			st.State = "resolved"
		}
		return st
	}
	return PTRStatus{State: "no_ip"}
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// chownTo is the injectable ownership hook (os.Chown in production).
func (r *Runner) chownTo(path string, uid, gid uint32) error {
	if r.chownFn != nil {
		return r.chownFn(path, int(uid), int(gid))
	}
	return os.Chown(path, int(uid), int(gid))
}

// vmail resolves the documented vmail uid/gid (injectable for tests).
func (r *Runner) vmail() (uid, gid uint32, err error) {
	if r.vmailLookup != nil {
		return r.vmailLookup()
	}
	u, lerr := user.Lookup("vmail")
	if lerr != nil {
		return 0, 0, ErrNoVmailUser
	}
	iv, perr := strconv.ParseUint(u.Uid, 10, 32)
	if perr != nil {
		return 0, 0, ErrNoVmailUser
	}
	ig, gerr := strconv.ParseUint(u.Gid, 10, 32)
	if gerr != nil {
		return 0, 0, ErrNoVmailUser
	}
	return uint32(iv), uint32(ig), nil
}
