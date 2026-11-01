//go:build unix

package webops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeExec records argv history and serves canned results. Production code
// never spawns a shell; tests never spawn anything at all.
type fakeExec struct {
	mu     sync.Mutex
	calls  []string
	failOn map[string]error // "bin arg1 arg2" → error
	output map[string]string
}

func (f *fakeExec) run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	if err, ok := f.failOn[key]; ok {
		return []byte("hata: " + key), err
	}
	return []byte(f.output[key]), nil
}

func (f *fakeExec) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == key {
			return true
		}
	}
	return false
}

func (f *fakeExec) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// fakeDB is the injectable user/group database.
type fakeDB struct {
	users  map[string]*user.User
	groups map[string]*user.Group
}

func (d *fakeDB) user(name string) (*user.User, error) {
	if u, ok := d.users[name]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("kullanıcı yok: %s", name)
}

func (d *fakeDB) group(name string) (*user.Group, error) {
	if g, ok := d.groups[name]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("grup yok: %s", name)
}

// testEnv wires a fully hermetic Runner: temp dirs, fake exec/user/group,
// no real binaries.
type testEnv struct {
	r     *Runner
	ex    *fakeExec
	db    *fakeDB
	conf  string
	web   string
	pools string // fmt: %s → pool dir
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	conf := filepath.Join(root, "conf.d")
	web := filepath.Join(root, "www")
	poolFmt := filepath.Join(root, "php", "%s", "fpm", "pool.d")
	ex := &fakeExec{failOn: map[string]error{}, output: map[string]string{}}
	db := &fakeDB{users: map[string]*user.User{}, groups: map[string]*user.Group{
		"www-data": {Gid: "33", Name: "www-data"},
	}}
	r := NewRunner()
	r.ConfDir, r.WebRoot, r.PHPPoolFmt = conf, web, poolFmt
	r.execFn = ex.run
	r.lookPath = func(bin string) (string, error) {
		if bin == DefaultNginxBin {
			return "/usr/sbin/nginx", nil
		}
		return "", fmt.Errorf("lookPath: %s yok", bin)
	}
	r.lookupUser = db.user
	r.lookupGroup = db.group
	r.listenersFn = func() ([]Listener, error) { return nil, nil }
	r.chownFn = func(string, int, int) error { return nil }
	// useradd/userdel mutate the fake DB (argv stays recorded).
	baseExec := ex.run
	r.execFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		// nginx -T dump: reflect the managed configs ACTUALLY on disk
		// (realistic inclusion proof for the fake).
		if name == DefaultNginxBin && len(args) == 1 && args[0] == "-T" {
			_, _ = baseExec(ctx, name, args...) // argv kaydı (test kanıtı)
			entries, _ := os.ReadDir(conf)
			var b strings.Builder
			b.WriteString(MarkerHeader + "\n")
			for _, en := range entries {
				fn := en.Name()
				if strings.HasPrefix(fn, ConfPrefix) && strings.HasSuffix(fn, ".conf") {
					if raw, err := os.ReadFile(filepath.Join(conf, fn)); err == nil {
						if site, _, _, _, _ := ParseSiteConfig(raw); site != "" {
							b.WriteString(metaSite + site + "\n")
						}
					}
				}
			}
			return []byte(b.String()), nil
		}
		if name == DefaultUseradd && len(args) > 0 {
			u := args[len(args)-1]
			db.users[u] = &user.User{Uid: "1500", Gid: "1500", Username: u, HomeDir: filepath.Join(web, strings.TrimPrefix(u, UserPrefix))}
		}
		if name == DefaultUserdel && len(args) > 0 {
			delete(db.users, args[len(args)-1])
		}
		return baseExec(ctx, name, args...)
	}
	e := &testEnv{r: r, ex: ex, db: db, conf: conf, web: web, pools: poolFmt}
	// nginx -T dump contains the managed marker metadata (inclusion proof).
	ex.output["nginx -T"] = MarkerHeader + "\n# site: zzz\n"
	return e
}

func (e *testEnv) poolDir(v string) string {
	p := fmt.Sprintf(e.pools, v)
	if err := os.MkdirAll(p, 0o755); err != nil {
		panic(err)
	}
	return p
}

func (e *testEnv) staticSpec() SiteSpec {
	return SiteSpec{Name: "blog", ServerNames: []string{"blog.example.com"}, Backend: BackendStatic, ListenPort: 8080}
}

func (e *testEnv) phpSpec() SiteSpec {
	e.poolDir("8.3")
	return SiteSpec{Name: "shop", ServerNames: []string{"shop.example.com"}, Backend: BackendPHP, PHPVersion: "8.3", MaxChildren: 10, ListenPort: 8081}
}

func TestCreateStaticHappyPath(t *testing.T) {
	e := newTestEnv(t)
	res, err := e.r.Create(context.Background(), e.staticSpec())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.Executed {
		t.Fatal("ilk create Executed=true olmalı")
	}
	// Config file with marker.
	raw, err := os.ReadFile(res.ConfigPath)
	if err != nil || !HasMarker(raw) {
		t.Fatalf("config yok/marker yok: %v", err)
	}
	if res.User != "sc-web-blog" {
		t.Fatalf("user = %q", res.User)
	}
	if _, ok := e.db.users["sc-web-blog"]; !ok {
		t.Fatal("site kullanıcısı oluşturulmalı")
	}
	if _, err := os.Stat(res.DocRoot); err != nil {
		t.Fatalf("docroot yok: %v", err)
	}
	// argv discipline: syntax check BEFORE reload; inclusion proof ran.
	calls := e.ex.count("nginx -t")
	if calls != 1 {
		t.Fatalf("nginx -t tam 1 kez çağrılmalı: %d", calls)
	}
	if !e.ex.has("nginx -T") {
		t.Fatal("nginx -T inclusion proof koşmalı")
	}
	if !e.ex.has("systemctl reload-or-try-restart nginx") {
		t.Fatal("nginx reload sinyali verilmeli")
	}
	if e.ex.has("systemctl reload-or-try-restart php8.3-fpm.service") {
		t.Fatal("static backend php-fpm reload yapmamalı")
	}
}

func TestCreateIdempotentNoop(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	useraddCalls := e.ex.count("useradd")
	res, err := e.r.Create(context.Background(), e.staticSpec())
	if err != nil {
		t.Fatal(err)
	}
	if res.Executed {
		t.Fatal("aynı içerikle ikinci create Executed=false olmalı")
	}
	if got := e.ex.count("useradd"); got != useraddCalls {
		t.Fatalf("useradd tekrar çağrıldı: %d→%d", useraddCalls, got)
	}
}

func TestCreateForeignConfigRefused(t *testing.T) {
	e := newTestEnv(t)
	foreign := "# operator's handcrafted config\nserver { listen 9999; }\n"
	if err := os.MkdirAll(e.conf, 0o755); err != nil {
		t.Fatal(err)
	}
	path := ConfFileName(e.conf, "blog")
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.r.Create(context.Background(), e.staticSpec())
	if !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("ErrForeignConfig beklenir: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != foreign {
		t.Fatal("yabancı config DEĞİŞMEMELİ (kör yazım yasağı)")
	}
	if e.ex.has("systemctl reload-or-try-restart nginx") {
		t.Fatal("yabancı config reddedilirken reload YAPILMAMALI")
	}
}

func TestCreateSyntaxFailRollsBack(t *testing.T) {
	e := newTestEnv(t)
	e.ex.failOn["nginx -t"] = errors.New("nginx: configuration file test failed")
	_, err := e.r.Create(context.Background(), e.staticSpec())
	if !errors.Is(err, ErrSyntaxCheck) {
		t.Fatalf("ErrSyntaxCheck beklenir: %v", err)
	}
	// Fresh file removed; NO reload signal.
	if _, serr := os.Stat(ConfFileName(e.conf, "blog")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("syntax hatasında yeni config geri alınmalı")
	}
	if e.ex.has("systemctl reload-or-try-restart nginx") {
		t.Fatal("syntax check geçmeden reload YAPILMAMALI (kriter 3)")
	}
}

func TestCreateSyntaxFailRestoresRollbackPool(t *testing.T) {
	e := newTestEnv(t)
	// Pre-existing pool file (managed content, older version) so the
	// rollback path is real.
	poolDir := e.poolDir("8.3")
	poolPath := PHPPoolPath(poolDir, "shop")
	old := "; " + MarkerHeader + "\n; eski pool\n"
	if err := os.WriteFile(poolPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	e.ex.failOn["nginx -t"] = errors.New("boom")
	_, err := e.r.Create(context.Background(), e.phpSpec())
	if !errors.Is(err, ErrSyntaxCheck) {
		t.Fatalf("ErrSyntaxCheck beklenir: %v", err)
	}
	raw, _ := os.ReadFile(poolPath)
	if string(raw) != old {
		t.Fatalf("pool rollback geri yüklenmeli; içerik: %q", raw)
	}
}

func TestCreatePortConflictApache(t *testing.T) {
	e := newTestEnv(t)
	e.r.listenersFn = func() ([]Listener, error) {
		return []Listener{{Port: 8080, Owner: "apache2"}}, nil
	}
	spec := e.staticSpec() // listen 8080
	_, err := e.r.Create(context.Background(), spec)
	if !errors.Is(err, ErrPortConflict) {
		t.Fatalf("ErrPortConflict beklenir: %v", err)
	}
	if _, serr := os.Stat(ConfFileName(e.conf, "blog")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("çakışmada config YAZILMAMALI")
	}
	if e.ex.has("systemctl reload-or-try-restart nginx") {
		t.Fatal("çakışmada reload YAPILMAMALI")
	}
}

func TestCreatePortOwnedByNginxAllowed(t *testing.T) {
	e := newTestEnv(t)
	e.r.listenersFn = func() ([]Listener, error) {
		return []Listener{{Port: 8080, Owner: "nginx"}}, nil
	}
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatalf("nginx'e ait portta vhost kurulabilmeli: %v", err)
	}
}

func TestCreateUnknownOwnerRefused(t *testing.T) {
	e := newTestEnv(t)
	e.r.listenersFn = func() ([]Listener, error) {
		return []Listener{{Port: 8080, Owner: ""}}, nil
	}
	_, err := e.r.Create(context.Background(), e.staticSpec())
	if !errors.Is(err, ErrPortConflict) {
		t.Fatalf("sahipsiz dinleyici güvenli tarafta reddedilmeli: %v", err)
	}
}

func TestCreatePHPNotInstalled(t *testing.T) {
	e := newTestEnv(t)
	spec := SiteSpec{Name: "shop", ServerNames: []string{"s.com"}, Backend: BackendPHP, PHPVersion: "8.3"}
	_, err := e.r.Create(context.Background(), spec)
	if !errors.Is(err, ErrPHPNotInstalled) {
		t.Fatalf("ErrPHPNotInstalled beklenir: %v", err)
	}
}

func TestCreatePHPHappyPath(t *testing.T) {
	e := newTestEnv(t)
	res, err := e.r.Create(context.Background(), e.phpSpec())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.PoolPath)
	if err != nil || !HasMarker(raw) {
		t.Fatalf("pool dosyası marker ile yazılmalı: %v", err)
	}
	if !strings.Contains(string(raw), "user = sc-web-shop") {
		t.Fatal("pool site kullanıcısıyla çalışmalı")
	}
	if !e.ex.has("systemctl reload-or-try-restart php8.3-fpm.service") {
		t.Fatal("php-fpm reload sinyali verilmeli (kriter 4)")
	}
}

func TestCreateMatrisDisiPHPRefused(t *testing.T) {
	e := newTestEnv(t)
	spec := e.phpSpec()
	spec.PHPVersion = "7.4"
	if _, err := e.r.Create(context.Background(), spec); !errors.Is(err, ErrPHPUnsupported) {
		t.Fatalf("matris dışı sürüm reddedilmeli: %v", err)
	}
}

func TestUpdateWritesRollbackCopy(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	old := RenderNginxConfig(e.staticSpec())
	spec := e.staticSpec()
	spec.ListenPort = 8090
	res, err := e.r.Update(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Executed || res.RollbackPath == "" {
		t.Fatalf("update Executed=true + rollback döndürmeli: %+v", res)
	}
	rb, err := os.ReadFile(res.RollbackPath)
	if err != nil || !bytes.Equal(rb, old) {
		t.Fatalf("rollback kopyası ESKİ içeriği tutmalı: %v", err)
	}
	// New content is live.
	raw, _ := os.ReadFile(res.ConfigPath)
	if !strings.Contains(string(raw), "listen 8090;") {
		t.Fatal("yeni config etkin olmalı")
	}
}

func TestUpdateMissingNotFound(t *testing.T) {
	e := newTestEnv(t)
	_, err := e.r.Update(context.Background(), e.staticSpec())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ErrNotFound beklenir: %v", err)
	}
}

func TestUpdateKeepsOwnPort(t *testing.T) {
	// Site owns 8080 (its own socket); update keeps the port → allowed
	// even when the listener attributes to a non-nginx entry.
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	e.r.listenersFn = func() ([]Listener, error) {
		return []Listener{{Port: 8080, Owner: "other-proc"}}, nil
	}
	spec := e.staticSpec()
	spec.ServerNames = []string{"changed.example.com"}
	if _, err := e.r.Update(context.Background(), spec); err != nil {
		t.Fatalf("kendi portu üzerinde update serbest: %v", err)
	}
}

func TestDisableAndReenableViaCreate(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	res, err := e.r.Disable(context.Background(), "blog")
	if err != nil || !res.Executed {
		t.Fatalf("Disable: %v %+v", err, res)
	}
	disabled := ConfFileName(e.conf, "blog") + DisabledSuffix
	if _, err := os.Stat(disabled); err != nil {
		t.Fatalf("disabled kopya yok: %v", err)
	}
	if _, err := os.Stat(ConfFileName(e.conf, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("etkin config kaldırılmalı")
	}
	// Second disable: honest no-op.
	res2, err := e.r.Disable(context.Background(), "blog")
	if err != nil || res2.Executed {
		t.Fatalf("ikinci disable no-op olmalı: %v %+v", err, res2)
	}
}

func TestDisableSyntaxFailReverts(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	e.ex.failOn["nginx -t"] = errors.New("boom")
	if _, err := e.r.Disable(context.Background(), "blog"); !errors.Is(err, ErrSyntaxCheck) {
		t.Fatalf("ErrSyntaxCheck beklenir: %v", err)
	}
	if _, err := os.Stat(ConfFileName(e.conf, "blog")); err != nil {
		t.Fatal("syntax hatasında disable geri alınmalı (site etkin kalır)")
	}
	if _, err := os.Stat(ConfFileName(e.conf, "blog") + DisabledSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled kopya temizlenmeli")
	}
}

func TestDeletePreservesDocroot(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.phpSpec()); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(e.web, "shop", "html", "index.php")
	if err := os.WriteFile(sentinel, []byte("<?php echo 1;"), 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := e.r.Delete(context.Background(), "shop")
	if err != nil || !res.Executed {
		t.Fatalf("Delete: %v %+v", err, res)
	}
	if !res.DocPreserved {
		t.Fatal("DocPreserved=true olmalı (kullanıcı verisi korunur)")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("docroot içeriği SİLİNMEMELİ")
	}
	if _, err := os.Stat(res.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("config kaldırılmalı")
	}
	if _, err := os.Stat(res.PoolPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pool kaldırılmalı")
	}
	if res.RollbackPath == "" {
		t.Fatal("delete rollback kopyası bırakmalı")
	}
	if _, err := os.Stat(res.RollbackPath); err != nil {
		t.Fatalf("rollback dosyası yok: %v", err)
	}
	if !e.ex.has("userdel sc-web-shop") {
		t.Fatal("site kullanıcısı silinmeli")
	}
	// Second delete: not found.
	if _, err := e.r.Delete(context.Background(), "shop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci delete ErrNotFound: %v", err)
	}
}

func TestDeleteSyntaxFailRestores(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	e.ex.failOn["nginx -t"] = errors.New("boom")
	if _, err := e.r.Delete(context.Background(), "blog"); !errors.Is(err, ErrSyntaxCheck) {
		t.Fatalf("ErrSyntaxCheck beklenir: %v", err)
	}
	if _, err := os.Stat(ConfFileName(e.conf, "blog")); err != nil {
		t.Fatal("syntax hatasında config geri yüklenmeli")
	}
}

func TestListAndStatus(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Create(context.Background(), e.phpSpec()); err != nil {
		t.Fatal(err)
	}
	rows, err := e.r.List(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("2 site beklenir: %+v", rows)
	}
	byName := map[string]SiteInfo{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if !byName["blog"].Enabled || byName["blog"].Backend != BackendStatic || byName["blog"].Port != 8080 {
		t.Fatalf("blog satırı yanlış: %+v", byName["blog"])
	}
	if byName["shop"].PHPVersion != "8.3" || !byName["shop"].UserExists || !byName["shop"].DocExists {
		t.Fatalf("shop satırı yanlış: %+v", byName["shop"])
	}
	st, err := e.r.Status(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if st.ConfigPath == "" || st.PoolPath == "" || st.HasRollback {
		t.Fatalf("status yanlış: %+v", st)
	}
	if _, err := e.r.Status(context.Background(), "yok"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan site ErrNotFound: %v", err)
	}
	if _, err := e.r.List(context.Background(), 0); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit 0 ErrLimit: %v", err)
	}
}

func TestListSkipsForeignAndDisabledDuplicate(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.staticSpec()); err != nil {
		t.Fatal(err)
	}
	// Foreign files in the shared conf.d must be ignored (never touched).
	if err := os.WriteFile(filepath.Join(e.conf, "zzz.conf"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Malformed managed-prefixed file: skipped, not parsed, not mutated.
	if err := os.WriteFile(filepath.Join(e.conf, ConfPrefix+"broken.conf"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := e.r.List(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "blog" {
		t.Fatalf("yalnız yönetilen site listelenmeli: %+v", rows)
	}
	// Disable → list still reports exactly one (disabled) row.
	if _, err := e.r.Disable(context.Background(), "blog"); err != nil {
		t.Fatal(err)
	}
	rows, err = e.r.List(context.Background(), 100)
	if err != nil || len(rows) != 1 || rows[0].Enabled {
		t.Fatalf("disabled site listelenmeli ama etkin olmamalı: %+v err=%v", rows, err)
	}
}

func TestPHPVersionsMatrix(t *testing.T) {
	e := newTestEnv(t)
	e.poolDir("8.2")
	e.poolDir("8.3")
	m, err := e.r.PHPVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Supported) != len(PHPSupported) {
		t.Fatal("matris raporu kapalı küme olmalı")
	}
	got := map[string]bool{}
	for _, st := range m.Installed {
		got[st.Version] = true
		if st.Unit != "php"+st.Version+"-fpm.service" {
			t.Fatalf("unit adı yanlış: %+v", st)
		}
	}
	if !got["8.2"] || !got["8.3"] || got["8.4"] {
		t.Fatalf("kurulu sürüm tespiti yanlış: %v", got)
	}
}

func TestPHPVersionsScansPools(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.r.Create(context.Background(), e.phpSpec()); err != nil {
		t.Fatal(err)
	}
	m, err := e.r.PHPVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pools) != 1 || m.Pools[0].Site != "shop" || m.Pools[0].Version != "8.3" {
		t.Fatalf("pool taraması yanlış: %+v", m.Pools)
	}
}

func TestListenersProcFixture(t *testing.T) {
	root := t.TempDir()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1\n"
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(tcp), 0o644); err != nil {
		t.Fatal(err)
	}
	pidDir := filepath.Join(root, "101")
	fdDir := filepath.Join(pidDir, "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[4242]", filepath.Join(fdDir, "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "comm"), []byte("nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRunner()
	r.procRoot = root
	ls, err := r.listeners()
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Port != 80 || ls[0].Owner != "nginx" {
		t.Fatalf("proc fixture yanlış: %+v err=%v", ls, err)
	}
}

func TestInputNeverReachesArgvUnvalidated(t *testing.T) {
	// A hostile site name never survives validation, so it can never reach
	// useradd/systemctl argv (defense in depth, ADR-0012).
	hostile := []string{"x;rm", "a b", "--flag", "$(id)", "a\nb", "../x"}
	for _, h := range hostile {
		if ValidSiteName(h) {
			t.Fatalf("düşman adı kabul edildi: %q", h)
		}
		spec := SiteSpec{Name: h, ServerNames: []string{"a.com"}, Backend: BackendStatic}
		if err := spec.Validate(); err == nil {
			t.Fatalf("spec kabul edildi: %q", h)
		}
	}
}

func TestParseProcNetInodes(t *testing.T) {
	body := []byte("  0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1\n")
	inodes, err := parseProcNetInodes(body)
	if err != nil {
		t.Fatal(err)
	}
	if inodes["12345"] != 80 {
		t.Fatalf("inode→port yanlış: %v", inodes)
	}
}
