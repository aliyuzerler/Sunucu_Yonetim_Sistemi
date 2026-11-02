//go:build linux

// Faz 10 gerçek-host kanıt testi (dosya yöneticisi): gerçek docroot + gerçek
// site kullanıcısı üzerinde upload/download/chmod/chown yaşam döngüsü ve
// gerçek symlink-escape reddi. EXPECTED_FS=1 gate'i CI-17 dersidir: env set
// edildiğinde gerçek kanıt ZORUNLUDUR — sessiz atlanamaz. CI agent-dbfs
// job'ında koşar.
package fsops

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireFSGate(t *testing.T) {
	t.Helper()
	if os.Getenv("EXPECTED_FS") != "1" {
		t.Skip("EXPECTED_FS=1 gerekir (CI agent-dbfs job'ı; yerelde atlanır)")
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		t.Fatalf("EXPECTED_FS=1 ama useradd yok — sessiz atlama YASAK (CI-17): %v", err)
	}
}

func TestRealFSLifecycle(t *testing.T) {
	requireFSGate(t)
	site := "citest"
	user := "sc-web-citest"

	// Site kullanıcısı (webops ile aynı argv disiplini).
	if out, err := exec.Command("id", "-u", user).Output(); err != nil { //nolint:gosec // fixed argv
		if out2, err2 := exec.Command("sudo", "useradd", "--system", "--user-group",
			"--shell", "/usr/sbin/nologin", user).CombinedOutput(); err2 != nil { //nolint:gosec // fixed argv
			t.Fatalf("useradd: %v %s", err2, out2)
		}
		_ = out
	}
	// docroot: /var/www/server-control/citest/html
	r := &Runner{} // gerçek DefaultWebRoot
	doc := r.siteRootDir(site)
	if out, err := exec.Command("sudo", "mkdir", "-p", doc).CombinedOutput(); err != nil { //nolint:gosec // fixed argv
		t.Fatalf("mkdir: %v %s", err, out)
	}
	if out, err := exec.Command("sudo", "chown", user+":"+user, doc).CombinedOutput(); err != nil { //nolint:gosec // fixed argv
		t.Fatalf("chown docroot: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("sudo", "userdel", user).Run()                              //nolint:gosec // fixed argv
		_ = exec.Command("sudo", "rm", "-rf", "/var/www/server-control/"+site).Run() //nolint:gosec // test cleanup
	})

	ctx := t.Context()
	up, err := r.Upload(ctx, site, "index.html", b64([]byte("<h1>citest</h1>")), 0o644, false)
	if err != nil || !up.Created {
		t.Fatalf("upload: %+v err=%v", up, err)
	}
	if _, err := r.Upload(ctx, site, "index.html", b64([]byte("<h1>v2</h1>")), 0o644, true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	dl, err := r.Download(ctx, site, "index.html")
	if err != nil || dl.Size == 0 || dl.Content == "" {
		t.Fatalf("download: %+v err=%v", dl, err)
	}
	if _, err := r.Chmod(ctx, site, "index.html", 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cw, err := r.Chown(ctx, site, "index.html")
	if err != nil || cw.User != user {
		t.Fatalf("chown: %+v err=%v", cw, err)
	}
	lst, err := r.List(ctx, site, "", 500)
	if err != nil || len(lst.Entries) == 0 {
		t.Fatalf("list: %+v err=%v", lst, err)
	}
	// gerçek symlink-escape reddi: docroot'ta /etc'ye link, download reddi.
	if err := os.Symlink("/etc/passwd", filepath.Join(doc, "escape.link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := r.Download(ctx, site, "escape.link"); err != ErrSymlink {
		t.Fatalf("gerçek symlink escape reddedilmeli: %v", err)
	}
}
