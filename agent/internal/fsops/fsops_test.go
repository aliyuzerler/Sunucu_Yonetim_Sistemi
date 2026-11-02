//go:build linux

package fsops

// Güvenlik negatif testleri (faz teslimatı) + hermetik yaşam döngüsü.
// Amaç (kriter 4+5): traversal/symlink/race/size/MIME kapıları ve site
// köküne hapsedilme kanıtlanır — host geneli asla browse edilemez.

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func setupSite(t *testing.T) (*Runner, string) {
	t.Helper()
	base := t.TempDir()
	site := "blog"
	doc := filepath.Join(base, site, "html")
	if err := os.MkdirAll(filepath.Join(doc, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(doc, "a.txt"), []byte("hello fs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(doc, "sub", "b.txt"), []byte("nested"), 0o640); err != nil {
		t.Fatal(err)
	}
	// symlink escape örnekleri: dosya + dizin
	if err := os.Symlink("/etc/passwd", filepath.Join(doc, "pass.link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr", filepath.Join(doc, "out.dir")); err != nil {
		t.Fatal(err)
	}
	// iç symlink (kapsam içinde): list'de görünür ama işlem yapılamaz
	if err := os.Symlink("a.txt", filepath.Join(doc, "a.link")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(doc)
	if err != nil {
		t.Fatal(err)
	}
	uid := 0
	gid := 0
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		uid, gid = int(sys.Uid), int(sys.Gid)
	}
	r := &Runner{
		WebRoot: base,
		SiteUserFn: func(string) (int, int, error) {
			return uid, gid, nil
		},
	}
	return r, doc
}

// ---- doğrulama katmanı ----

func TestValidRelPath(t *testing.T) {
	for _, p := range []string{"a.txt", "sub/b.txt", "a-b_c.d", ""} {
		if _, err := ValidRelPath(p); err != nil {
			t.Errorf("geçerli sayılmalı %q: %v", p, err)
		}
	}
	for _, p := range []string{
		"/abs", "a/", "../x", "a/../b", "a/..", ".", "..", "a/./b",
		"a//b", strings.Repeat("a", 97), strings.Repeat("s/", 17) + "x",
		"boo t", "bo'o", strings.Repeat("a", 401),
	} {
		if _, err := ValidRelPath(p); err == nil {
			t.Errorf("reddedilmeli: %q", p)
		}
	}
}

func TestParseMode(t *testing.T) {
	m, err := ParseMode("0644")
	if err != nil || m != 0o644 {
		t.Fatalf("0644: %v %v", m, err)
	}
	m, err = ParseMode("644") // önsıfırsız da oktal
	if err != nil || m != 0o644 {
		t.Fatalf("644: %v %v", m, err)
	}
	for _, bad := range []string{"1777", "4755", "2755", "9999", "abc", "07777"} {
		if _, err := ParseMode(bad); err == nil {
			t.Errorf("reddedilmeli: %q", bad)
		}
	}
}

func TestCheckUploadPolicy(t *testing.T) {
	if _, err := CheckUploadPolicy("shell.php", []byte("<?php echo 1;")); err != ErrForbiddenExt {
		t.Fatalf(".php reddedilmeli: %v", err)
	}
	if _, err := CheckUploadPolicy("run.sh", []byte("#!/bin/sh\n")); err != ErrForbiddenExt {
		t.Fatalf(".sh reddedilmeli: %v", err)
	}
	if _, err := CheckUploadPolicy("secret.key", []byte("-----BEGIN")); err != ErrForbiddenExt {
		t.Fatalf(".key reddedilmeli: %v", err)
	}
	// uzantı masum ama içerik shebang → MIME katmanı reddeder
	if _, err := CheckUploadPolicy("notes.txt", []byte("#!/bin/sh\nrm -rf /")); err != ErrForbiddenMime {
		t.Fatalf("shebang içerik reddedilmeli: %v", err)
	}
	if m, err := CheckUploadPolicy("page.html", []byte("<h1>hi</h1>")); err != nil || m == "" {
		t.Fatalf("html kabul: %v %q", err, m)
	}
}

func TestDecodeContent(t *testing.T) {
	big := make([]byte, RawFileCap+1)
	if _, err := DecodeContent(b64(big)); err != ErrTooLarge {
		t.Fatalf("büyük içerik ErrTooLarge: %v", err)
	}
	if _, err := DecodeContent("!!!not-base64!!!"); err != ErrInvalidPayload {
		t.Fatalf("bozuk base64: %v", err)
	}
	out, err := DecodeContent(b64([]byte("ok")))
	if err != nil || string(out) != "ok" {
		t.Fatalf("normal içerik: %v", err)
	}
}

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// ---- runner yaşam döngüsü + negatif güvenlik yolları ----

func TestListAndDownload(t *testing.T) {
	r, _ := setupSite(t)
	ctx := context.Background()

	res, err := r.List(ctx, "blog", "", 500)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Truncated || len(res.Entries) != 5 { // a.txt, a.link, out.dir, pass.link, sub
		t.Fatalf("kök liste: %+v", res)
	}
	// symlink girdileri görünür ama link hedefi ile
	byName := map[string]Entry{}
	for _, e := range res.Entries {
		byName[e.Name] = e
	}
	if byName["pass.link"].Type != "symlink" || byName["pass.link"].Link != "/etc/passwd" {
		t.Fatalf("symlink girdisi: %+v", byName["pass.link"])
	}
	if byName["a.txt"].Type != "file" || byName["a.txt"].Size != 8 {
		t.Fatalf("dosya girdisi: %+v", byName["a.txt"])
	}

	// alt dizin listesi
	sub, err := r.List(ctx, "blog", "sub", 500)
	if err != nil || len(sub.Entries) != 1 || sub.Entries[0].Name != "b.txt" {
		t.Fatalf("sub liste: %+v err=%v", sub, err)
	}

	// truncate sınırı dürüst
	lim, err := r.List(ctx, "blog", "", 2)
	if err != nil || !lim.Truncated || len(lim.Entries) != 2 {
		t.Fatalf("limit=2: %+v err=%v", lim, err)
	}

	// download
	dl, err := r.Download(ctx, "blog", "a.txt")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if dl.Size != 8 || dl.Mime == "" || dl.SHA256 == "" {
		t.Fatalf("download sonucu: %+v", dl)
	}
}

func TestSecurityNegatives(t *testing.T) {
	r, doc := setupSite(t)
	ctx := context.Background()

	// symlink escape: dosya download → ErrSymlink (O_NOFOLLOW)
	if _, err := r.Download(ctx, "blog", "pass.link"); err != ErrSymlink {
		t.Fatalf("symlink download reddedilmeli: %v", err)
	}
	// symlink escape: dizin listesi (intermediate symlink) → ErrSymlink
	if _, err := r.List(ctx, "blog", "out.dir", 500); err != ErrSymlink {
		t.Fatalf("symlink dizin reddedilmeli: %v", err)
	}
	// iç symlink de işlem göremez
	if _, err := r.Download(ctx, "blog", "a.link"); err != ErrSymlink {
		t.Fatalf("iç symlink reddedilmeli: %v", err)
	}
	// traversal: ValidRelPath zaten reddeder; runner'a kadar gitmez
	if _, err := r.Download(ctx, "blog", "../escaped.txt"); err == nil {
		t.Fatal("traversal reddedilmeli")
	}
	// mutlak yol reddedilir
	if _, err := r.List(ctx, "blog", "/etc", 500); err == nil {
		t.Fatal("mutlak yol reddedilmeli")
	}
	// kapsam dışı kök: geçerli ad ama docroot yok → ErrNotSiteRoot
	if _, err := r.List(ctx, "nosuchsite", "", 500); err == nil {
		t.Fatal("olmayan site reddedilmeli")
	}
	// geçersiz kök adı → ErrInvalidRoot
	if _, err := r.List(ctx, "../etc", "", 500); err == nil {
		t.Fatal("geçersiz kök reddedilmeli")
	}
	// boyut sınırı: 600 KiB dosya → ErrTooLarge
	big := filepath.Join(doc, "big.bin")
	if err := os.WriteFile(big, make([]byte, 600<<10), 0o644); err != nil { //nolint:gosec // test path
		t.Fatal(err)
	}
	if _, err := r.Download(ctx, "blog", "big.bin"); err != ErrTooLarge {
		t.Fatalf("büyük dosya reddedilmeli: %v", err)
	}
}

func TestUploadChmodChownLifecycle(t *testing.T) {
	r, doc := setupSite(t)
	ctx := context.Background()

	// create
	up, err := r.Upload(ctx, "blog", "sub/c.txt", b64([]byte("new content")), 0o640, false)
	if err != nil || !up.Created {
		t.Fatalf("upload: %+v err=%v", up, err)
	}
	// replay no-overwrite → ErrExists
	if _, err := r.Upload(ctx, "blog", "sub/c.txt", b64([]byte("x")), 0o640, false); err != ErrExists {
		t.Fatalf("replay ErrExists: %v", err)
	}
	// overwrite
	up2, err := r.Upload(ctx, "blog", "sub/c.txt", b64([]byte("replaced")), 0o600, true)
	if err != nil || !up2.Overwritten {
		t.Fatalf("overwrite: %+v err=%v", up2, err)
	}
	got, err := os.ReadFile(filepath.Join(doc, "sub", "c.txt")) //nolint:gosec // test path
	if err != nil || string(got) != "replaced" {
		t.Fatalf("overwrite içerik: %q %v", got, err)
	}
	st, _ := os.Stat(filepath.Join(doc, "sub", "c.txt")) //nolint:gosec // test path
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("overwrite mod: %v", st.Mode().Perm())
	}

	// forbidden upload
	if _, err := r.Upload(ctx, "blog", "evil.php", b64([]byte("<?php")), 0o644, false); err != ErrForbiddenExt {
		t.Fatalf(".php upload reddedilmeli: %v", err)
	}

	// chmod
	ch, err := r.Chmod(ctx, "blog", "a.txt", 0o600)
	if err != nil || ch.NewMode != "0600" {
		t.Fatalf("chmod: %+v err=%v", ch, err)
	}
	st2, _ := os.Stat(filepath.Join(doc, "a.txt")) //nolint:gosec // test path
	if st2.Mode().Perm() != 0o600 {
		t.Fatalf("chmod uygulanmadı: %v", st2.Mode().Perm())
	}
	// chmod symlink → ErrSymlink
	if _, err := r.Chmod(ctx, "blog", "a.link", 0o600); err != ErrSymlink {
		t.Fatalf("symlink chmod reddedilmeli: %v", err)
	}

	// chown: hedef KULLANICI GİRDİSİ YOK — site kullanıcısı türetilir
	cw, err := r.Chown(ctx, "blog", "sub/c.txt")
	if err != nil {
		t.Fatalf("chown: %v", err)
	}
	if cw.UID == 0 && cw.GID == 0 && cw.User == "" {
		t.Fatalf("chown sonucu boş: %+v", cw)
	}
	if cw.User != "sc-web-blog" {
		t.Fatalf("chown hedefi türetilmiş kullanıcı olmalı: %q", cw.User)
	}

	// geçici upload dosyaları kalmamalı
	entries, _ := os.ReadDir(filepath.Join(doc, "sub")) //nolint:gosec // test path
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".srvctl-upload-") {
			t.Fatalf("temp kalıntısı: %s", e.Name())
		}
	}
}

// Site kullanıcısı yoksa chown dürüst tipli red verir (tahmin yok).
func TestChownWithoutSiteUser(t *testing.T) {
	r, _ := setupSite(t)
	r.SiteUserFn = nil // gerçek user.Lookup → sc-web-blog yok
	if _, err := r.Chown(context.Background(), "blog", "a.txt"); err == nil {
		t.Fatal("site kullanıcısı yokken chown reddedilmeli")
	}
}
