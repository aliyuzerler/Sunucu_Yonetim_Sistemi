//go:build linux

// Hermetic unit tests for the Faz 11 mail adapter. ALL external programs
// are fake shell shims in a temp directory (fixed argv asserted via a call
// log), so every branch — including syntax-check failure and rollback — is
// testable without Postfix/Dovecot installed (ADR-0015 test discipline).
package mailops

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shims builds a fake tool directory: postfix/postmap/postconf/postqueue/
// doveconf/doveadm/rspamadm scripts that log their argv and behave per env
// flags. Returns the Runner wired to them plus the log path.
func shims(t *testing.T) (r *Runner, logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "calls.log")
	write := func(name, body string) string {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	postfix := write("postfix", `
echo "postfix $@" >> "$LOG"
case "$1" in
  check) [ -f "$CHECK_FAIL" ] && { echo "postfix: fatal: bad config" >&2; exit 1; }; exit 0;;
  reload) [ -f "$RELOAD_FAIL" ] && exit 1; exit 0;;
esac
exit 0`)
	postmap := write("postmap", `
echo "postmap $@" >> "$LOG"
touch "$1.db"
exit 0`)
	postconf := write("postconf", `
echo "postconf $@" >> "$LOG"
case "$1" in
  -h) echo "10240000"; exit 0;;
  -e) [ -f "$POSTCONF_FAIL" ] && exit 1; exit 0;;
esac
exit 0`)
	postqueue := write("postqueue", `
echo "postqueue $@" >> "$LOG"
if [ -n "$POSTQUEUE_JSON" ]; then cat "$POSTQUEUE_JSON"; fi
exit 0`)
	doveconf := write("doveconf", `
echo "doveconf $@" >> "$LOG"
[ -f "$DOVECONF_FAIL" ] && { echo "doveconf: fatal" >&2; exit 1; }
exit 0`)
	doveadm := write("doveadm", `echo "doveadm $@" >> "$LOG"; exit 0`)
	rspamadm := write("rspamadm", `echo "rspamadm $@" >> "$LOG"; exit 0`)
	systemctl := write("systemctl", `echo "systemctl $@" >> "$LOG"; echo active; exit 0`)

	base := t.TempDir()
	mainCFDir := t.TempDir()
	mainCF := filepath.Join(mainCFDir, "main.cf")
	if err := os.WriteFile(mainCF, []byte("# base config\nmyhostname = test.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vmailBase := t.TempDir()
	t.Setenv("LOG", logPath)
	for _, kv := range []string{"CHECK_FAIL", "RELOAD_FAIL", "POSTCONF_FAIL", "DOVECONF_FAIL"} {
		t.Setenv(kv, "")
	}
	t.Setenv("POSTQUEUE_JSON", "")
	r = &Runner{
		Postfix: postfix, Postmap: postmap, Postqueue: postqueue, Postconf: postconf,
		Doveconf: doveconf, Doveadm: doveadm, Rspamadm: rspamadm, Systemctl: systemctl,
		BaseDir: base, PostfixMainCF: mainCF,
		DovecotConfDir: filepath.Join(t.TempDir(), "conf.d"),
		VMailBase:      vmailBase,
	}
	r.vmailLookup = func() (uid, gid uint32, err error) { return 5000, 5000, nil }
	// Sandbox test user cannot chown to vmail; production broker runs as root.
	r.chownFn = func(path string, uid, gid int) error { return nil }
	return r, logPath
}

func calls(t *testing.T, logPath, prefix string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func fileBytes(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestValidDomain(t *testing.T) {
	ok := []string{"example.com", "mail.example.com", "a-b.co", "x1.y2.z3.io"}
	for _, d := range ok {
		if !ValidDomain(d) {
			t.Errorf("ValidDomain(%q) = false, want true", d)
		}
	}
	bad := []string{"", "Example.com", "example", "-a.b", "a-.b", "a..b",
		"ex ample.com", strings.Repeat("a", 64) + ".com", "a_b.com", "http://x.com"}
	for _, d := range bad {
		if ValidDomain(d) {
			t.Errorf("ValidDomain(%q) = true, want false", d)
		}
	}
}

func TestValidLocal(t *testing.T) {
	ok := []string{"a", "user1", "first.last", "a-b_c", "u9"}
	for _, l := range ok {
		if !ValidLocal(l) {
			t.Errorf("ValidLocal(%q) = false, want true", l)
		}
	}
	bad := []string{"", ".a", "a.", "a..b", "-a", "A", "a b", strings.Repeat("a", 65)}
	for _, l := range bad {
		if ValidLocal(l) {
			t.Errorf("ValidLocal(%q) = true, want false", l)
		}
	}
}

func TestValidAddress(t *testing.T) {
	if !ValidAddress("user@example.com") {
		t.Error("ValidAddress(user@example.com) = false")
	}
	for _, a := range []string{"userexample.com", "@example.com", "user@", "us er@x.com", "User@x.com"} {
		if ValidAddress(a) {
			t.Errorf("ValidAddress(%q) = true, want false", a)
		}
	}
}

func TestValidQuotaAndLimits(t *testing.T) {
	if !ValidQuota(int64(1) << 30) {
		t.Error("1 GiB quota must be valid")
	}
	if ValidQuota(int64(1<<20) - 1) {
		t.Error("below-min quota must be invalid")
	}
	if ValidQuota((int64(1) << 40) + 1) {
		t.Error("above-max quota must be invalid")
	}
	if !ValidLimitKey("message_size_limit", 10<<20) {
		t.Error("message_size_limit 10 MiB must be valid")
	}
	if ValidLimitKey("message_size_limit", 200<<20) {
		t.Error("message_size_limit above max must be invalid")
	}
	if ValidLimitKey("not_a_real_key", 1) {
		t.Error("unknown key must be invalid (closed enum)")
	}
	if got := len(LimitKeyNames()); got != 6 {
		t.Errorf("LimitKeyNames = %d, want 6", got)
	}
}

func TestHashSSHA(t *testing.T) {
	h1, err := HashSSHA("secret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "{SSHA}") {
		t.Fatalf("hash prefix = %q, want {SSHA}", h1[:7])
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h1, "{SSHA}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 20+16 {
		t.Fatalf("decoded hash len = %d, want 36 (sha1+salt)", len(raw))
	}
	h2, _ := HashSSHA("secret-password")
	if h1 == h2 {
		t.Error("two hashes of the same password must differ (random salt)")
	}
}

func TestUserLineRoundTrip(t *testing.T) {
	line := UserLine("u@example.com", "{SSHA}abc=", 5000, 5000, "/var/vmail/e.com/u/Maildir", 1<<30)
	// Documented passwd-file format: user:pass:uid:gid:gecos:home:shell:extra
	// → shell boş, extra alanına iki nokta üst üste konur.
	fields := strings.Split(line, ":")
	if len(fields) < 8 || fields[6] != "" {
		t.Fatalf("line violates the documented 8-field format: %q", line)
	}
	addr, home, quota, ok := ParseUserLine(line)
	if !ok || addr != "u@example.com" || home != "/var/vmail/e.com/u/Maildir" || quota != 1<<30 {
		t.Fatalf("round trip mismatch: %q %q %d %v", addr, home, quota, ok)
	}
	if _, _, _, ok := ParseUserLine("garbage"); ok {
		t.Error("malformed line must not parse")
	}
}

func TestDomainCreateAppliesAndCompiles(t *testing.T) {
	r, log := shims(t)
	ctx := context.Background()
	res, err := r.DomainCreate(ctx, "example.com")
	if err != nil {
		t.Fatalf("DomainCreate: %v", err)
	}
	if !res.Applied || !res.Reloaded {
		t.Fatalf("applied/reloaded = %v/%v, want true/true", res.Applied, res.Reloaded)
	}
	lines := r.readMapLines(r.domainPath())
	if len(lines) != 1 || lines[0] != "example.com" {
		t.Fatalf("domains file = %v, want [example.com]", lines)
	}
	// postmap + check + reload all ran (fixed argv, no shell). First run
	// compiles the THREE wiring maps (empty), then the apply compiles 1 → 4.
	if got := len(calls(t, log, "postmap ")); got != 4 {
		t.Fatalf("postmap calls = %d, want 4", got)
	}
	if len(calls(t, log, "postfix check")) < 1 {
		t.Fatal("postfix check must run")
	}
	mainCF := fileBytes(t, r.mainCF())
	if !strings.Contains(string(mainCF), MainCFBegin) || !strings.Contains(string(mainCF), "virtual_mailbox_domains = hash:"+r.BaseDir) {
		t.Fatal("managed main.cf block missing")
	}
	if strings.Contains(string(mainCF), "static:0") {
		t.Fatal("vmail uid/gid must be the injected non-zero values")
	}
}

func TestDomainCreateDuplicate(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	if _, err := r.DomainCreate(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DomainCreate(ctx, "example.com"); err != ErrExists {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestDomainCreateSyntaxFailRollsBack(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	if _, err := r.DomainCreate(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}
	before := fileBytes(t, r.domainPath())
	t.Setenv("CHECK_FAIL", "/proc/1/environ") // any existing file → check fails
	if _, err := r.DomainCreate(ctx, "b.example"); err != ErrSyntaxCheck {
		t.Fatalf("err = %v, want ErrSyntaxCheck", err)
	}
	after := fileBytes(t, r.domainPath())
	if string(before) != string(after) {
		t.Fatalf("rollback failed: file changed\nbefore=%q\nafter=%q", before, after)
	}
	if strings.Contains(string(after), "b.example") {
		t.Fatal("rolled-back file must not contain the new domain")
	}
}

func TestDomainDeleteConflictAndHappy(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	if _, err := r.DomainCreate(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	// Mailbox varken domain silinemez (koruyucu sıra).
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DomainDelete(ctx, "example.com"); err == nil ||
		!strings.Contains(err.Error(), "mailbox") {
		t.Fatalf("err = %v, want conflict mentioning mailbox", err)
	}
	// Önce mailbox silinir.
	if _, err := r.MailboxDelete(ctx, "user@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DomainDelete(ctx, "example.com"); err != nil {
		t.Fatalf("DomainDelete: %v", err)
	}
	if len(r.readMapLines(r.domainPath())) != 0 {
		t.Fatal("domains file must be empty after delete")
	}
	if _, err := r.DomainDelete(ctx, "example.com"); err != ErrNotFound {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestMailboxCreateFullFlow(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	if _, err := r.DomainCreate(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	res, err := r.MailboxCreate(ctx, "example.com", "user")
	if err != nil {
		t.Fatalf("MailboxCreate: %v", err)
	}
	cred, _ := res["credentials"].(map[string]any)
	if cred == nil || cred["password"] == "" {
		t.Fatal("credentials.password must be present exactly once in the result")
	}
	pw := cred["password"].(string)
	if len(pw) != PasswordLength {
		t.Fatalf("password len = %d, want %d", len(pw), PasswordLength)
	}
	users := fileBytes(t, r.usersPath())
	if !strings.Contains(string(users), "user@example.com:{SSHA}") {
		t.Fatalf("userdb entry missing:\n%s", users)
	}
	if strings.Contains(string(users), pw) {
		t.Fatal("PLAINTEXT password must NEVER be written to the userdb (only the hash)")
	}
	vb := string(fileBytes(t, r.vmailboxPath()))
	if !strings.Contains(vb, "user@example.com example.com/user/Maildir/") {
		t.Fatalf("vmailbox entry missing:\n%s", vb)
	}
	md := filepath.Join(r.VMailBase, "example.com", "user", "Maildir")
	st, err := os.Stat(md)
	if err != nil || !st.IsDir() {
		t.Fatalf("Maildir missing: %v", err)
	}
	// Dovecot yönetilen parçası yazıldı ve doveconf -n kapısından geçti.
	conf := string(fileBytes(t, filepath.Join(r.DovecotConfDir, DovecotConfName)))
	if !strings.Contains(conf, "passwd-file") || !strings.Contains(conf, r.BaseDir) {
		t.Fatalf("dovecot fragment missing/incomplete:\n%s", conf)
	}
	// Maildir izinleri 0700 ve sahibi vmail (5000:5000).
	info, _ := os.Stat(md)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("Maildir perm = %v, want 0700", info.Mode().Perm())
	}
}

func TestMailboxCreateRequiresManagedDomain(t *testing.T) {
	r, _ := shims(t)
	if _, err := r.MailboxCreate(context.Background(), "ghost.example", "user"); err == nil ||
		!strings.Contains(err.Error(), "yönetilen listede yok") {
		t.Fatalf("err = %v, want managed-domain refusal", err)
	}
}

func TestMailboxListNoSecrets(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	if _, err := r.DomainCreate(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != nil {
		t.Fatal(err)
	}
	list, err := r.MailboxList(ctx, "", 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if list[0].Address != "user@example.com" || list[0].QuotaBytes != DefaultQuotaBytes {
		t.Fatalf("mailbox info = %+v", list[0])
	}
	raw := string(fileBytes(t, r.usersPath()))
	if strings.Contains(string(raw), "password") {
		t.Error("userdb must contain hashes, not the word/password itself")
	}
}

func TestMailboxDeleteRemovesEverything(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != nil {
		t.Fatal(err)
	}
	res, err := r.MailboxDelete(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("MailboxDelete: %v", err)
	}
	if res["maildir_removed"] != true {
		t.Fatalf("maildir_removed = %v, want true", res["maildir_removed"])
	}
	if _, err := os.Stat(filepath.Join(r.VMailBase, "example.com", "user")); !os.IsNotExist(err) {
		t.Fatal("Maildir tree must be removed")
	}
	if len(r.readMapLines(r.vmailboxPath())) != 0 || len(r.readMapLines(r.usersPath())) != 0 {
		t.Fatal("maps/userdb must be empty after delete")
	}
	if _, err := r.MailboxDelete(ctx, "user@example.com"); err != ErrNotFound {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestAliasLocalTargetMustExist(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	if _, err := r.AliasCreate(ctx, "info@example.com", "ghost@example.com"); err == nil ||
		!strings.Contains(err.Error(), "hedef mailbox yok") {
		t.Fatalf("err = %v, want local-target refusal", err)
	}
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AliasCreate(ctx, "info@example.com", "user@example.com"); err != nil {
		t.Fatalf("AliasCreate: %v", err)
	}
	// Dış hedef serbest (deliverability dış DNS bağımlılığı gibi dürüst kapsam).
	if _, err := r.AliasCreate(ctx, "abuse@example.com", "admin@external.example"); err != nil {
		t.Fatalf("external target rejected: %v", err)
	}
	list, err := r.AliasList(ctx, "example.com", 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("alias list = %v err=%v", list, err)
	}
	if _, err := r.AliasDelete(ctx, "info@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AliasDelete(ctx, "info@example.com"); err != ErrNotFound {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestAliasDuplicateRejected(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	_, _ = r.MailboxCreate(ctx, "example.com", "user")
	if _, err := r.AliasCreate(ctx, "info@example.com", "user@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AliasCreate(ctx, "info@example.com", "user@example.com"); err != ErrExists {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestQuotaSet(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QuotaSet(ctx, "user@example.com", int64(500)<<20); err != nil {
		t.Fatalf("QuotaSet: %v", err)
	}
	list, _ := r.MailboxList(ctx, "", 0)
	if list[0].QuotaBytes != int64(500)<<20 {
		t.Fatalf("quota = %d, want %d", list[0].QuotaBytes, int64(500)<<20)
	}
	if _, err := r.QuotaSet(ctx, "user@example.com", 1024); err != ErrInvalidQuota {
		t.Fatalf("small quota err = %v, want ErrInvalidQuota", err)
	}
	if _, err := r.QuotaSet(ctx, "ghost@example.com", 1<<30); err != ErrNotFound {
		t.Fatalf("ghost quota err = %v, want ErrNotFound", err)
	}
}

func TestConfigCheck(t *testing.T) {
	r, _ := shims(t)
	res, err := r.ConfigCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Postfix.State != "pass" || res.Dovecot.State != "pass" || res.Rspamd.State != "pass" {
		t.Fatalf("config check = %+v", res)
	}
}

func TestQueueListDropsContent(t *testing.T) {
	r, _ := shims(t)
	j := filepath.Join(t.TempDir(), "q.json")
	body := `{"queue_name":"deferred","queue_id":"ABC123","arrival_time":1700000000,"message_size":2048,"sender":"bounce@example.com","recipients":[{"address":"secret-rcpt@hidden.example","delay_reason":"connect to mx: Connection refused"}]}
{"queue_name":"incoming","queue_id":"DEF456","arrival_time":1700000100,"message_size":512,"sender":"a@b.c","recipients":[]}
not-json-line
` + "\n"
	if err := os.WriteFile(j, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POSTQUEUE_JSON", j)
	entries, err := r.QueueList(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (malformed line skipped)", len(entries))
	}
	e := entries[0]
	if e.QueueID != "ABC123" || e.SizeBytes != 2048 || e.Sender != "bounce@example.com" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Reason == "" || !strings.Contains(e.Reason, "Connection refused") {
		t.Fatalf("reason = %q", e.Reason)
	}
	// Alıcı adresleri ASLA sonuçta görünmez (kriter 5).
	for _, en := range entries {
		if strings.Contains(en.Sender, "secret-rcpt") || strings.Contains(en.Reason, "secret-rcpt") {
			t.Fatal("recipient address leaked into the queue view")
		}
	}
	// Limit uygulanır.
	t.Setenv("POSTQUEUE_JSON", j)
	entries, _ = r.QueueList(context.Background(), 1)
	if len(entries) != 1 {
		t.Fatalf("limit ignored: %d", len(entries))
	}
}

func TestLimitsGetSet(t *testing.T) {
	r, log := shims(t)
	ctx := context.Background()
	got, err := r.LimitsGet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Limits["message_size_limit"] != "10240000" {
		t.Fatalf("limits get = %v", got.Limits)
	}
	res, err := r.LimitsSet(ctx, map[string]int64{
		"message_size_limit":   20 << 20,
		"anvil_rate_time_unit": 300,
	})
	if err != nil {
		t.Fatalf("LimitsSet: %v", err)
	}
	if !res.Applied || !res.Reloaded || len(res.Keys) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if len(calls(t, log, "postfix reload")) != 1 {
		t.Fatal("postfix reload must run after a successful apply")
	}
	// Kapalı enum: bilinmeyen anahtar tipli red.
	if _, err := r.LimitsSet(ctx, map[string]int64{"evil_key": 1}); err == nil {
		t.Fatal("unknown key must be rejected")
	}
	// Aralık: aşırı değer tipli red.
	if _, err := r.LimitsSet(ctx, map[string]int64{"message_size_limit": 1 << 30}); err == nil {
		t.Fatal("out-of-range value must be rejected")
	}
}

func TestLimitsSetSyntaxFailRestoresMainCF(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	before := fileBytes(t, r.mainCF())
	t.Setenv("CHECK_FAIL", "/proc/1/environ")
	if _, err := r.LimitsSet(ctx, map[string]int64{"message_size_limit": 20 << 20}); err != ErrSyntaxCheck {
		t.Fatalf("err = %v, want ErrSyntaxCheck", err)
	}
	if string(fileBytes(t, r.mainCF())) != string(before) {
		t.Fatal("main.cf must be restored byte-identically after a failed check")
	}
}

func TestStatus(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	_, _ = r.MailboxCreate(ctx, "example.com", "user")
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Domains != 1 || st.Mailboxes != 1 || st.Aliases != 0 {
		t.Fatalf("counts = %+v", st)
	}
	if st.Services["postfix"] != "active" {
		t.Fatalf("postfix service = %q", st.Services["postfix"])
	}
	if !st.PostfixWired {
		t.Fatal("postfix must be reported wired after mutations")
	}
}

func TestPreflightReportsHonestState(t *testing.T) {
	r, _ := shims(t)
	res, err := r.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.PostfixInstalled || !res.DovecotInstalled || !res.RspamdInstalled {
		t.Fatalf("binaries = %+v", res.Binaries)
	}
	if !res.VmailUser {
		t.Fatal("injected vmail lookup must be reported")
	}
	if len(res.Ports) != len(PreflightPorts) {
		t.Fatalf("ports = %d, want %d", len(res.Ports), len(PreflightPorts))
	}
	for _, p := range res.Ports {
		if p.State != "free" && p.State != "occupied" && p.State != "unknown" {
			t.Fatalf("port state = %q", p.State)
		}
	}
	if res.PostfixWired {
		t.Fatal("fresh environment must report postfix NOT wired")
	}
}

func TestMailboxCreateDoveconfFailRollsBack(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	_, _ = r.DomainCreate(ctx, "example.com")
	// userdb apply sırasında doveconf fail → mailbox yarım kalmaz.
	// Not: domain.create da doveconf kullanmaz; yalnız mailbox userdb yazar.
	t.Setenv("DOVECONF_FAIL", "/proc/1/environ")
	if _, err := r.MailboxCreate(ctx, "example.com", "user"); err != ErrSyntaxCheck {
		t.Fatalf("err = %v, want ErrSyntaxCheck", err)
	}
	// vmailbox geri alındı (userdb apply başarısız olduğu için).
	if len(r.readMapLines(r.vmailboxPath())) != 0 {
		t.Fatalf("vmailbox must be restored, got %v", r.readMapLines(r.vmailboxPath()))
	}
	// userdb boş (hash asla yarım kalmaz).
	if len(r.readMapLines(r.usersPath())) != 0 {
		t.Fatalf("userdb must be restored, got %v", r.readMapLines(r.usersPath()))
	}
	// Maildir oluşmadı (yalnız tüm kapılar geçince).
	if _, err := os.Stat(filepath.Join(r.VMailBase, "example.com", "user")); !os.IsNotExist(err) {
		t.Fatal("Maildir must not exist after a failed syntax gate")
	}
}

func TestReloadFailureIsHonestNotError(t *testing.T) {
	r, _ := shims(t)
	ctx := context.Background()
	t.Setenv("RELOAD_FAIL", "/proc/1/environ")
	res, err := r.DomainCreate(ctx, "example.com")
	if err != nil {
		t.Fatalf("valid config must apply even when the daemon is down: %v", err)
	}
	if !res.Applied || res.Reloaded {
		t.Fatalf("applied/reloaded = %v/%v, want true/false (honest)", res.Applied, res.Reloaded)
	}
}

func TestAtomicWritePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := atomicWrite(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", st.Mode().Perm())
	}
	// Temp kalıntısı yok.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir = %d entries, want 1", len(entries))
	}
}
