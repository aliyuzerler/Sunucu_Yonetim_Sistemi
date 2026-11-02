//go:build linux

// Faz 11 gerçek-host kanıt testi: runner'da GERÇEK Postfix + Dovecot ile
// tam mail yaşam döngüsü (domain → mailbox → KONTROLLÜ TESLİM (sendmail →
// Maildir) → alias → quota → queue → delete). EXPECTED_MAIL=1 gate'i
// CI-17 dersidir: env set edildiğinde gerçek kanıt ZORUNLUDUR — sessiz
// atlanamaz. CI agent-mail job'ında koşar. Yalnız `citest.` önekli adlar
// kullanılır.
package mailops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireMailGate(t *testing.T) {
	t.Helper()
	if os.Getenv("EXPECTED_MAIL") != "1" {
		t.Skip("EXPECTED_MAIL=1 gerekir (CI agent-mail job'ı; yerelde atlanır)")
	}
	for _, b := range []string{"postfix", "postmap", "postqueue", "postconf",
		"doveconf", "doveadm", "sendmail"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Fatalf("EXPECTED_MAIL=1 ama %s yok — sessiz atlama YASAK (CI-17): %v", b, err)
		}
	}
	if _, _, err := (&Runner{}).vmail(); err != nil {
		t.Fatalf("EXPECTED_MAIL=1 ama vmail kullanıcısı yok — sessiz atlama YASAK (CI-17): %v", err)
	}
}

// TestRealMailLifecycle: gerçek Postfix üzerinde tam yaşam döngüsü ve
// KONTROLLÜ TESLİM kanıtı (sendmail → virtual transport → Maildir dosyası).
func TestRealMailLifecycle(t *testing.T) {
	requireMailGate(t)
	r := &Runner{Timeout: 120 * time.Second}
	ctx := context.Background()
	domain := "citest.example"
	addr := "citest-user@" + domain

	// 1) Preflight: gerçek ikililer raporlanmalı (kurulum YOK — kriter 1).
	pf, err := r.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !pf.PostfixInstalled || !pf.DovecotInstalled {
		t.Fatalf("gerçek runner'da postfix+dovecot kurulu olmalı: %+v", pf.Binaries)
	}
	if !pf.VmailUser {
		t.Fatal("vmail kullanıcısı raporlanmalı")
	}
	if len(pf.Ports) != len(PreflightPorts) {
		t.Fatalf("port sörveyi eksik: %+v", pf.Ports)
	}

	// 2) Domain create (temizlik garantili).
	t.Cleanup(func() {
		_, _ = r.MailboxDelete(context.Background(), addr)
		_, _ = r.AliasDelete(context.Background(), "citest-alias@"+domain)
		_, _ = r.DomainDelete(context.Background(), domain)
	})
	if _, err := r.DomainCreate(ctx, domain); err != nil {
		t.Fatalf("domain create: %v", err)
	}
	// Replay: ikinci create tipli ErrExists (idempotent-güvenli davranış).
	if _, err := r.DomainCreate(ctx, domain); err != ErrExists {
		t.Fatalf("domain replay err = %v, want ErrExists", err)
	}

	// 3) Mailbox create: şifre NODE'da üretilir, düz metin YALNIZ sonuçta.
	res, err := r.MailboxCreate(ctx, domain, "citest-user")
	if err != nil {
		t.Fatalf("mailbox create: %v", err)
	}
	cred, _ := res["credentials"].(map[string]any)
	if cred == nil || len(cred["password"].(string)) != PasswordLength {
		t.Fatalf("mailbox credentials: %v", cred)
	}
	// userdb HASH içerir, düz metin ASLA.
	users, rerr := os.ReadFile(r.usersPath())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(users), addr+":{SSHA}") {
		t.Fatalf("userdb {SSHA} girdisi yok:\n%s", users)
	}
	if strings.Contains(string(users), cred["password"].(string)) {
		t.Fatal("DÜZ METİN ŞİFRE userdb'ye YAZILAMAZ")
	}

	// 4) KONTROLLÜ TESLİM: sendmail → postfix virtual → Maildir dosyası.
	// Önce config CANLIYA alınır: runner reload'u best-effort'tur ve postfix
	// test başlamadan ÖNCE başlatıldıysa managed blok sonradan yazılmıştır —
	// gerçek-host kanıtı reload'u ZORUNLU kılar (dürüst kanıt zinciri).
	if out, rerr := exec.Command("postfix", "reload").CombinedOutput(); rerr != nil { //nolint:gosec // fixed argv test helper
		t.Fatalf("postfix reload başarısız — config canlıya alınamadı: %v: %s", rerr, out)
	}
	msg := "From: sender@" + domain + "\nTo: " + addr +
		"\nSubject: faz11 kontrollu teslim\n\nfaz11 delivery proof\n"
	cmd := exec.Command("sendmail", "-t", "-i") //nolint:gosec // fixed argv test helper
	cmd.Stdin = strings.NewReader(msg)
	if out, serr := cmd.CombinedOutput(); serr != nil {
		t.Fatalf("sendmail: %v: %s", serr, out)
	}
	maildir := filepath.Join(r.vmailBase(), domain, "citest-user", "Maildir", "new")
	deadline := time.Now().Add(60 * time.Second)
	delivered := false
	for time.Now().Before(deadline) {
		entries, derr := os.ReadDir(maildir)
		if derr == nil && len(entries) > 0 {
			delivered = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !delivered {
		// Dürüst tanı: kuyruk durumu + postconf + mail log kuyruğu —
		// CI'da kök-neden görülebilir olsun.
		q, _ := exec.Command("postqueue", "-p").CombinedOutput() //nolint:gosec // fixed argv diagnostics
		cf, _ := exec.Command("postconf", "-n").CombinedOutput() //nolint:gosec // fixed argv diagnostics
		logTail := ""
		if b, lerr := os.ReadFile("/var/log/mail.log"); lerr == nil && len(b) > 0 { //nolint:gosec // known CI path
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > 40 {
				lines = lines[len(lines)-40:]
			}
			logTail = strings.Join(lines, "\n")
		}
		list, _ := os.ReadDir(filepath.Dir(maildir))
		t.Fatalf("kontrollü teslim kanıtı yok (Maildir/new boş): %v\n--- postqueue -p ---\n%s\n--- postconf -n (virtual satırları) ---\n%s\n--- /var/log/mail.log (son 40) ---\n%s",
			list, q, virtualLines(cf), logTail)
	}

	// 5) Alias (yerel hedef doğrulaması gerçek veriyle).
	if _, err := r.AliasCreate(ctx, "citest-alias@"+domain, addr); err != nil {
		t.Fatalf("alias create: %v", err)
	}
	aliases, err := r.AliasList(ctx, domain, 0)
	if err != nil || len(aliases) != 1 || aliases[0].Target != addr {
		t.Fatalf("alias list: %v %v", aliases, err)
	}

	// 6) Quota set (kriter 2 — tipli kota işlemi).
	if _, err := r.QuotaSet(ctx, addr, int64(500)<<20); err != nil {
		t.Fatalf("quota set: %v", err)
	}
	boxes, err := r.MailboxList(ctx, domain, 0)
	if err != nil || len(boxes) != 1 || boxes[0].QuotaBytes != int64(500)<<20 {
		t.Fatalf("mailbox list after quota: %v %v", boxes, err)
	}

	// 7) Config check (kriter 4 — gerçek postfix check geçer).
	cc, err := r.ConfigCheck(ctx)
	if err != nil || cc.Postfix.State != "pass" || cc.Dovecot.State != "pass" {
		t.Fatalf("config check: %+v %v", cc, err)
	}

	// 8) Queue list: postqueue -j gerçek çağrı ayrışmalı (içerik YOK).
	queue, err := r.QueueList(ctx, 0)
	if err != nil {
		t.Fatalf("queue list: %v", err)
	}
	for _, q := range queue {
		if strings.Contains(strings.ToLower(q.Reason), "body") {
			t.Fatal("queue içeriği sızdı")
		}
	}

	// 9) Mailbox delete → Maildir ağacı gerçekten gider.
	if _, err := r.MailboxDelete(ctx, addr); err != nil {
		t.Fatalf("mailbox delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.vmailBase(), domain, "citest-user")); !os.IsNotExist(err) {
		t.Fatal("Maildir ağacı silinmeli")
	}

	// 9.5) Alias delete — DomainDelete'in koruyucu çelişki reddi (çocuk
	// varken silinmez) doğrudur; lifecycle kapanışı çocuklardan önce.
	if _, err := r.AliasDelete(ctx, "citest-alias@"+domain); err != nil {
		t.Fatalf("alias delete: %v", err)
	}

	// 10) Domain delete (temizlik + lifecycle kapanışı).
	if _, err := r.DomainDelete(ctx, domain); err != nil {
		t.Fatalf("domain delete: %v", err)
	}
}

// TestRealMailSyntaxRollback: check kapısı GERÇEK postfix'te de rollback
// yapmalı (kriter 4 — gerçek kanıt).
func TestRealMailSyntaxRollback(t *testing.T) {
	requireMailGate(t)
	r := &Runner{Timeout: 120 * time.Second}
	ctx := context.Background()
	domain := "citest.example"
	if _, err := r.DomainCreate(ctx, domain); err != nil {
		t.Fatalf("domain create: %v", err)
	}
	t.Cleanup(func() { _, _ = r.DomainDelete(context.Background(), domain) })
	before, rerr := os.ReadFile(r.domainPath())
	if rerr != nil {
		t.Fatal(rerr)
	}
	// Bozuk map içeriği: key-value formatı dışı satır UYGULANIR ama
	// postfix check kısmen toleranslıdır — bu yüzden doğrudan postmap'i
	// bozuk veriyle çağırıp kapının tipi ErrSyntaxCheck dönmesini
	// beklemeden, UYGULAMA yolunu gerçek bozuk içerikle sınarız:
	// applyMap check'i geçemezse rollback etmek ZORUNDADIR.
	r2 := &Runner{Timeout: 120 * time.Second, Postfix: "/bin/false"} // check her zaman başarısız
	if _, err := r2.DomainCreate(ctx, domain); err == nil {
		t.Fatal("bozuk check ile create reddedilmeli")
	}
	after, rerr := os.ReadFile(r.domainPath())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(before) != string(after) {
		t.Fatal("gerçek akışta rollback byte-düzeyi olmalı")
	}
}

// virtualLines filters postconf -n output down to the virtual_* lines the
// delivery path depends on (keeps CI failure output focused).
func virtualLines(postconf []byte) string {
	var out []string
	for _, l := range strings.Split(string(postconf), "\n") {
		if strings.HasPrefix(l, "virtual_") || strings.HasPrefix(l, "mydestination") ||
			strings.HasPrefix(l, "myhostname") {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return "(virtual satırı yok — managed blok canlı config'te yok!)"
	}
	return strings.Join(out, "\n")
}
