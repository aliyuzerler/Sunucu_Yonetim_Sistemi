//go:build unix

package webops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Default filesystem layout (overridable per Runner for hermetic tests).
const (
	DefaultConfDir    = "/etc/nginx/conf.d"
	DefaultWebRoot    = "/var/www/server-control"
	DefaultPHPPoolFmt = "/etc/php/%s/fpm/pool.d"
	DefaultNologin    = "/usr/sbin/nologin"
	DefaultNginxBin   = "nginx"
	DefaultSystemctl  = "systemctl"
	DefaultUseradd    = "useradd"
	DefaultUserdel    = "userdel"
)

// exec limits: every child output is bounded; every call is ctx-bounded.
const (
	execOutputLimit = 64 << 10
	fileReadLimit   = 1 << 20
	dumpReadLimit   = 2 << 20
	reloadVerb      = "reload-or-try-restart"
)

// Runner executes the Faz 08 web hosting operations with REAL side effects
// (useradd, config writes, nginx reloads) — it runs ONLY inside the
// privileged broker. Every external command is a fixed binary + argument
// vector (never a shell); user input reaches argv ONLY through validated
// identifiers (site name → derived user/path, closed enums, bounded ints).
type Runner struct {
	ConfDir    string
	WebRoot    string
	PHPPoolFmt string // fmt with version → pool dir
	Nologin    string
	NginxBin   string
	Systemctl  string
	Useradd    string
	Userdel    string
	NginxGroup string

	// Injectables (hermetic tests; nil = real implementations).
	execFn      func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath    func(string) (string, error)
	lookupUser  func(string) (*user.User, error)
	lookupGroup func(string) (*user.Group, error)
	listenersFn func() ([]Listener, error)
	procRoot    string // /proc root for the listener scan (tests)
	chownFn     func(string, int, int) error
}

// NewRunner builds a Runner with the default system layout.
func NewRunner() *Runner {
	return &Runner{
		ConfDir:    DefaultConfDir,
		WebRoot:    DefaultWebRoot,
		PHPPoolFmt: DefaultPHPPoolFmt,
		Nologin:    DefaultNologin,
		NginxBin:   DefaultNginxBin,
		Systemctl:  DefaultSystemctl,
		Useradd:    DefaultUseradd,
		Userdel:    DefaultUserdel,
		NginxGroup: NginxGroup,
	}
}

func (r *Runner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.execFn != nil {
		return r.execFn(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed binary + validated argv, never shell
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.Bytes()
	if len(out) > execOutputLimit {
		out = out[:execOutputLimit]
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s: %s", name, msg)
	}
	return out, nil
}

func (r *Runner) paths() (confDir, webRoot, poolFmt string) {
	confDir, webRoot, poolFmt = r.ConfDir, r.WebRoot, r.PHPPoolFmt
	if confDir == "" {
		confDir = DefaultConfDir
	}
	if webRoot == "" {
		webRoot = DefaultWebRoot
	}
	if poolFmt == "" {
		poolFmt = DefaultPHPPoolFmt
	}
	return confDir, webRoot, poolFmt
}

func (r *Runner) nginxGroup() string {
	if r.NginxGroup != "" {
		return r.NginxGroup
	}
	return NginxGroup
}

func (r *Runner) userLookup(name string) (*user.User, error) {
	if r.lookupUser != nil {
		return r.lookupUser(name)
	}
	return user.Lookup(name)
}

func (r *Runner) groupLookup(name string) (*user.Group, error) {
	if r.lookupGroup != nil {
		return r.lookupGroup(name)
	}
	return user.LookupGroup(name)
}

func (r *Runner) look(bin string) (string, error) {
	if r.lookPath != nil {
		return r.lookPath(bin)
	}
	return exec.LookPath(bin)
}

// poolDirFor resolves the pool dir of one matrix version.
func (r *Runner) poolDirFor(version string) string {
	_, _, poolFmt := r.paths()
	return fmt.Sprintf(poolFmt, version)
}

// ---- environment checks ----

// nginxAvailable verifies the nginx binary exists (typed refusal otherwise).
func (r *Runner) nginxAvailable() error {
	if _, err := r.look(r.nginxBinName()); err != nil {
		return fmt.Errorf("%w: nginx bulunamadı (%s)", ErrUnavailable, r.nginxBinName())
	}
	return nil
}

func (r *Runner) nginxBinName() string {
	if r.NginxBin != "" {
		return r.NginxBin
	}
	return DefaultNginxBin
}

func (r *Runner) systemctlName() string {
	if r.Systemctl != "" {
		return r.Systemctl
	}
	return DefaultSystemctl
}

// phpInstalled reports whether the FPM layout of one matrix version exists.
func (r *Runner) phpInstalled(version string) bool {
	st, err := os.Stat(r.poolDirFor(version))
	return err == nil && st.IsDir()
}

// ---- low-level file ops ----

// readFileBounded reads a file with a hard size cap.
func readFileBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- path is derived from validated site names
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

// atomicWrite writes content via temp-file + rename (same dir → atomic on
// ext4/xfs/btrfs). If the target EXISTS it MUST carry our marker (foreign
// files are never touched) and its previous content is preserved as a
// `.rollback` copy. Returns (rollbackPath, changed, err).
func atomicWrite(path string, content []byte) (string, bool, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	existing, err := readFileBounded(path, fileReadLimit)
	switch {
	case err == nil:
		if !HasMarker(existing) {
			return "", false, ErrForeignConfig
		}
		if bytes.Equal(existing, content) {
			return "", false, nil // no-op: identical content
		}
		rollback := path + RollbackSuffix
		if err := os.WriteFile(rollback, existing, 0o644); err != nil {
			return "", false, err
		}
	case errors.Is(err, os.ErrNotExist):
		// new file
	default:
		return "", false, err
	}
	tmp, err := os.CreateTemp(dir, ".srvctl-atomic-*")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", false, err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", false, err
	}
	rb := path + RollbackSuffix
	if st, err := os.Stat(rb); err == nil && !st.IsDir() {
		return rb, true, nil
	}
	return "", true, nil
}

// restoreRollback reverts path to its rollback copy (or removes a fresh
// file). Best-effort; the primary error is returned alongside.
func restoreRollback(path, rollback string) error {
	if rollback == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	content, err := readFileBounded(rollback, fileReadLimit)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// ---- listener attribution (/proc/net/tcp{,6} + /proc/<pid>/fd) ----

// listeners scans listening TCP sockets and attributes each to its owning
// process comm (root-only information; the broker runs as root). On any
// read failure the verdict falls back to PortOwnerUnknown (conservative).
func (r *Runner) listeners() ([]Listener, error) {
	if r.listenersFn != nil {
		return r.listenersFn()
	}
	procRoot := r.procRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	inodeToPort := map[string]int{}
	for _, table := range []string{"tcp", "tcp6"} {
		raw, err := os.ReadFile(filepath.Join(procRoot, "net", table)) // #nosec G304 -- fixed /proc path
		if err != nil {
			continue
		}
		inodes, err := parseProcNetInodes(raw)
		if err != nil {
			continue
		}
		for ino, port := range inodes {
			inodeToPort[ino] = port
		}
	}
	if len(inodeToPort) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, nil
	}
	out := map[int]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == 0 {
			continue
		}
		fdDir := filepath.Join(procRoot, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		comm := ""
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			ino := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
			port, ok := inodeToPort[ino]
			if !ok {
				continue
			}
			if comm == "" {
				if c, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm")); err == nil {
					comm = strings.TrimSpace(string(c))
				}
			}
			if _, dup := out[port]; !dup {
				out[port] = comm
			}
		}
	}
	ports := make([]int, 0, len(out))
	for p := range out {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	ls := make([]Listener, 0, len(ports))
	for _, p := range ports {
		ls = append(ls, Listener{Port: p, Owner: out[p]})
	}
	return ls, nil
}

// parseProcNetInodes maps socket inode → listen port from one proc table.
func parseProcNetInodes(body []byte) (map[string]int, error) {
	out := map[string]int{}
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] == "sl" {
			continue
		}
		if f[3] != "0A" { // TCP_LISTEN only
			continue
		}
		_, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		if len(f) > 9 {
			if ino := strings.TrimSpace(f[9]); ino != "" && ino != "0" {
				out[ino] = int(port)
			}
		}
	}
	return out, nil
}

// checkPort enforces the port-conflict policy (criterion 5). allowOwnPort
// lets an UPDATE keep the port the site's own current config declares.
func (r *Runner) checkPort(spec SiteSpec, allowOwnPort bool) error {
	ls, err := r.listeners()
	if err != nil {
		// Conservative: cannot attribute → refuse when creating on a port we
		// do not already serve.
		return fmt.Errorf("%w: dinleyici bilgisi okunamadı: %v", ErrPortConflict, err)
	}
	verdict, owner := ClassifyPort(ls, spec.Normalize().ListenPort)
	switch verdict {
	case PortFree:
		return nil
	case PortOwnedByNginx:
		return nil // name-based vhost on a shared socket is normal
	case PortOwnedByApache, PortOwnedByOther, PortOwnerUnknown:
		if allowOwnPort && r.siteOwnsPort(spec.Name, spec.Normalize().ListenPort) {
			return nil
		}
		return SiteConflictError(verdict, owner, spec.Normalize().ListenPort)
	}
	return nil
}

// siteOwnsPort reports whether the site's OWN current config declares the
// port (update-in-place on the socket nginx already opened for it).
func (r *Runner) siteOwnsPort(site string, port int) bool {
	confDir, _, _ := r.paths()
	for _, p := range []string{ConfFileName(confDir, site), confDir + "/" + ConfPrefix + site + ".conf" + DisabledSuffix} {
		raw, err := readFileBounded(p, fileReadLimit)
		if err != nil {
			continue
		}
		n, _, _, prt, _ := ParseSiteConfig(raw)
		if n == site && prt == port {
			return true
		}
	}
	return false
}

// ---- user + docroot ----

// ensureSiteUser creates the per-site system user if missing (idempotent).
func (r *Runner) ensureSiteUser(ctx context.Context, name string) (string, error) {
	u := SiteUser(name)
	if _, err := r.userLookup(u); err == nil {
		return u, nil
	}
	home, _, _ := r.paths()
	home = filepath.Join(home, name)
	if _, err := r.run(ctx, r.useraddBinName(), "--system", "--user-group",
		"--home-dir", home, "--create-home", "--shell", r.nologinShell(), u); err != nil {
		return "", fmt.Errorf("site kullanıcısı oluşturulamadı: %w", err)
	}
	return u, nil
}

func (r *Runner) useraddBinName() string {
	if r.Useradd != "" {
		return r.Useradd
	}
	return DefaultUseradd
}

func (r *Runner) userdelBinName() string {
	if r.Userdel != "" {
		return r.Userdel
	}
	return DefaultUserdel
}

func (r *Runner) nologinShell() string {
	if r.Nologin != "" {
		return r.Nologin
	}
	return DefaultNologin
}

// ensureDocRoot creates the document root with site-user ownership and
// web-server group read+traverse (0750, no world access).
func (r *Runner) ensureDocRoot(name string) (string, error) {
	_, webRoot, _ := r.paths()
	root := filepath.Join(webRoot, name, "html")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	u, err := r.userLookup(SiteUser(name))
	if err != nil {
		return "", fmt.Errorf("site kullanıcısı okunamadı: %w", err)
	}
	g, err := r.groupLookup(r.nginxGroup())
	if err != nil {
		return "", fmt.Errorf("web sunucu grubu (%s) yok: %w", r.nginxGroup(), err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(g.Gid)
	chown := r.chownFn
	if chown == nil {
		chown = os.Chown
	}
	if err := chown(root, uid, gid); err != nil {
		return "", err
	}
	return root, nil
}

// ---- config lifecycle ----

// writeSiteConfig writes the nginx config atomically with marker + rollback
// guarantees. Fresh writes are verified afterwards via nginx -t / -T.
func (r *Runner) writeSiteConfig(spec SiteSpec) (rollback string, changed bool, err error) {
	confDir, _, _ := r.paths()
	path := ConfFileName(confDir, spec.Name)
	rb, changed, err := atomicWrite(path, RenderNginxConfig(spec))
	if err != nil {
		return "", false, err
	}
	return rb, changed, nil
}

// verifyAndReload runs the syntax gate + inclusion proof and only then
// signals the reload. On gate failure the rollback copy is restored (fresh
// files removed) and a typed error is returned — reload NEVER happens on a
// failed check (criterion 3).
func (r *Runner) verifyAndReload(ctx context.Context, site string, writes []rollbackable) error {
	if err := r.nginxAvailable(); err != nil {
		return err
	}
	if out, err := r.run(ctx, r.nginxBinName(), "-t"); err != nil {
		r.undo(writes)
		return fmt.Errorf("%w: %s", ErrSyntaxCheck, clamp(out))
	}
	out, err := r.run(ctx, r.nginxBinName(), "-T")
	if err != nil {
		r.undo(writes)
		return fmt.Errorf("%w: nginx -T çalıştırılamadı: %s", ErrSyntaxCheck, clamp(out))
	}
	if len(out) > dumpReadLimit {
		out = out[:dumpReadLimit]
	}
	if !bytes.Contains(out, []byte(metaSite+site)) {
		r.undo(writes)
		return fmt.Errorf("%w: site %s config dump içinde görünmüyor", ErrNotIncluded, site)
	}
	if _, err := r.run(ctx, r.systemctlName(), reloadVerb, "nginx"); err != nil {
		// Config is valid and included; the reload signal itself failed.
		// State is honest: files in place, reload reported as failure.
		return fmt.Errorf("nginx reload sinyali başarısız: %w", err)
	}
	return nil
}

// rollbackable is one written file with its rollback copy (empty rollback =
// fresh file → removed on undo).
type rollbackable struct {
	path     string
	rollback string
}

func (r *Runner) undo(writes []rollbackable) {
	for i := len(writes) - 1; i >= 0; i-- {
		_ = restoreRollback(writes[i].path, writes[i].rollback)
	}
}

func clamp(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// Create implements web.site.create (idempotent desired state).
func (r *Runner) Create(ctx context.Context, spec SiteSpec) (SiteResult, error) {
	spec = spec.Normalize()
	if err := spec.Validate(); err != nil {
		return SiteResult{}, err
	}
	if err := r.nginxAvailable(); err != nil {
		return SiteResult{}, err
	}
	if err := r.checkPort(spec, false); err != nil {
		return SiteResult{}, err
	}
	res := SiteResult{Site: spec.Name, Action: "create", Notes: []string{}}

	// PHP preconditions BEFORE any write (criterion 4: explicit validation).
	var poolPath string
	if spec.Backend == BackendPHP {
		if !r.phpInstalled(spec.PHPVersion) {
			return res, fmt.Errorf("%w: %s (pool dir %s yok)", ErrPHPNotInstalled, spec.PHPVersion, r.poolDirFor(spec.PHPVersion))
		}
		poolPath = PHPPoolPath(r.poolDirFor(spec.PHPVersion), spec.Name)
	}

	confDir, _, _ := r.paths()
	confPath := ConfFileName(confDir, spec.Name)
	if existing, err := readFileBounded(confPath, fileReadLimit); err == nil && HasMarker(existing) {
		// Site already exists: converge (update semantics) — identical
		// content reports an honest no-op.
		res.Executed = false
	} else if errors.Is(err, os.ErrNotExist) {
		_, statErr := os.Stat(confDir + "/" + ConfPrefix + spec.Name + ".conf" + DisabledSuffix)
		if statErr == nil {
			res.Notes = append(res.Notes, "devre dışı site bulundu; etkin config yeniden yazıldı")
		}
		res.Executed = true
	} else if err != nil {
		return res, err
	} else {
		return res, ErrForeignConfig
	}

	user1, err := r.ensureSiteUser(ctx, spec.Name)
	if err != nil {
		return res, err
	}
	res.User = user1
	docRoot, err := r.ensureDocRoot(spec.Name)
	if err != nil {
		return res, err
	}
	res.DocRoot = docRoot

	var writes []rollbackable
	if spec.Backend == BackendPHP {
		rb, changed, err := atomicWrite(poolPath, RenderPHPPool(spec))
		if err != nil {
			return res, err
		}
		if changed {
			writes = append(writes, rollbackable{poolPath, rb})
		}
		res.PoolPath = poolPath
		res.PHPVersion = spec.PHPVersion
	}
	rb, changed, err := r.writeSiteConfig(spec)
	if err != nil {
		r.undo(writes)
		return res, err
	}
	if changed {
		writes = append(writes, rollbackable{confPath, rb})
		if rb != "" {
			res.RollbackPath = rb
		}
	}
	res.ConfigPath = confPath

	if err := r.verifyAndReload(ctx, spec.Name, writes); err != nil {
		return res, err
	}
	if spec.Backend == BackendPHP {
		if _, err := r.run(ctx, r.systemctlName(), reloadVerb, PHPFPMUnit(spec.PHPVersion)); err != nil {
			// Pool file is valid for the matrix; FPM unit reload failed.
			return res, fmt.Errorf("php-fpm reload sinyali başarısız: %w", err)
		}
	}
	res.Executed = res.Executed || len(writes) > 0
	return res, nil
}

// Update implements web.site.update (convergent; re-enables a disabled
// site by writing the enabled config — documented behavior).
func (r *Runner) Update(ctx context.Context, spec SiteSpec) (SiteResult, error) {
	spec = spec.Normalize()
	if err := spec.Validate(); err != nil {
		return SiteResult{}, err
	}
	if err := r.nginxAvailable(); err != nil {
		return SiteResult{}, err
	}
	confDir, _, _ := r.paths()
	confPath := ConfFileName(confDir, spec.Name)
	disabledPath := confPath + DisabledSuffix
	_, enabledErr := os.Stat(confPath)
	_, disabledErr := os.Stat(disabledPath)
	if errors.Is(enabledErr, os.ErrNotExist) && errors.Is(disabledErr, os.ErrNotExist) {
		return SiteResult{Site: spec.Name, Action: "update"}, ErrNotFound
	}
	if err := r.checkPort(spec, true); err != nil {
		return SiteResult{}, err
	}
	if spec.Backend == BackendPHP && !r.phpInstalled(spec.PHPVersion) {
		return SiteResult{Site: spec.Name, Action: "update"}, fmt.Errorf("%w: %s", ErrPHPNotInstalled, spec.PHPVersion)
	}
	res := SiteResult{Site: spec.Name, Action: "update", Notes: []string{}}
	user1, err := r.ensureSiteUser(ctx, spec.Name)
	if err != nil {
		return res, err
	}
	res.User = user1
	docRoot, err := r.ensureDocRoot(spec.Name)
	if err != nil {
		return res, err
	}
	res.DocRoot = docRoot
	var writes []rollbackable
	executedAny := false
	if spec.Backend == BackendPHP {
		poolPath := PHPPoolPath(r.poolDirFor(spec.PHPVersion), spec.Name)
		rb, changed, err := atomicWrite(poolPath, RenderPHPPool(spec))
		if err != nil {
			return res, err
		}
		if changed {
			writes = append(writes, rollbackable{poolPath, rb})
			executedAny = true
		}
		res.PoolPath = poolPath
		res.PHPVersion = spec.PHPVersion
	}
	rb, changed, err := r.writeSiteConfig(spec)
	if err != nil {
		r.undo(writes)
		return res, err
	}
	if changed {
		writes = append(writes, rollbackable{confPath, rb})
		executedAny = true
		if rb != "" {
			res.RollbackPath = rb
		}
	} else {
		res.Notes = append(res.Notes, "config içeriği değişmedi")
	}
	res.Executed = executedAny
	res.ConfigPath = confPath
	// A disabled copy is superseded by the re-enabled config.
	if disabledErr == nil {
		_ = os.Remove(disabledPath)
		res.Notes = append(res.Notes, "devre dışı config kaldırıldı (site yeniden etkin)")
	}
	if err := r.verifyAndReload(ctx, spec.Name, writes); err != nil {
		return res, err
	}
	if spec.Backend == BackendPHP {
		if _, err := r.run(ctx, r.systemctlName(), reloadVerb, PHPFPMUnit(spec.PHPVersion)); err != nil {
			return res, fmt.Errorf("php-fpm reload sinyali başarısız: %w", err)
		}
	}
	return res, nil
}

// Disable implements web.site.disable (reversible rename; syntax gate +
// reload still enforced).
func (r *Runner) Disable(ctx context.Context, site string) (SiteResult, error) {
	if !ValidSiteName(site) {
		return SiteResult{}, fmt.Errorf("%w: %q", ErrInvalidName, site)
	}
	if err := r.nginxAvailable(); err != nil {
		return SiteResult{}, err
	}
	confDir, _, _ := r.paths()
	confPath := ConfFileName(confDir, site)
	disabledPath := confPath + DisabledSuffix
	if _, err := os.Stat(confPath); errors.Is(err, os.ErrNotExist) {
		if _, err2 := os.Stat(disabledPath); err2 == nil {
			return SiteResult{Site: site, Action: "disable", Executed: false,
				Notes: []string{"site zaten devre dışı"}}, nil
		}
		return SiteResult{Site: site, Action: "disable"}, ErrNotFound
	}
	// Rename first (the include glob stops matching), then gate + reload;
	// on ANY failure the rename is reverted (site stays enabled, honest error).
	if err := os.Rename(confPath, disabledPath); err != nil {
		return SiteResult{Site: site, Action: "disable"}, err
	}
	revert := func() { _ = os.Rename(disabledPath, confPath) }
	if out, err := r.run(ctx, r.nginxBinName(), "-t"); err != nil {
		revert()
		return SiteResult{Site: site, Action: "disable"}, fmt.Errorf("%w: %s", ErrSyntaxCheck, clamp(out))
	}
	if _, err := r.run(ctx, r.systemctlName(), reloadVerb, "nginx"); err != nil {
		revert()
		return SiteResult{Site: site, Action: "disable"}, fmt.Errorf("nginx reload sinyali başarısız: %w", err)
	}
	return SiteResult{Site: site, Action: "disable", Executed: true,
		ConfigPath: disabledPath, Notes: []string{"config " + DisabledSuffix + " uzantısıyla saklandı"}}, nil
}

// Delete implements web.site.delete. FILE-PRESERVING by design (ADR-0012):
// the document root is NEVER removed; only our marker-bearing artifacts
// (nginx config, FPM pool files) and the derived system user are removed —
// every removed file keeps a `.rollback` copy first.
func (r *Runner) Delete(ctx context.Context, site string) (SiteResult, error) {
	if !ValidSiteName(site) {
		return SiteResult{}, fmt.Errorf("%w: %q", ErrInvalidName, site)
	}
	confDir, webRoot, _ := r.paths()
	confPath := ConfFileName(confDir, site)
	disabledPath := confPath + DisabledSuffix

	res := SiteResult{Site: site, Action: "delete", Executed: true,
		DocRoot: filepath.Join(webRoot, site, "html"), Notes: []string{}}

	// Collect pool artifacts across all matrix versions.
	var poolPaths []string
	for _, v := range PHPSupported {
		p := PHPPoolPath(r.poolDirFor(v), site)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			poolPaths = append(poolPaths, p)
		}
	}

	confExists := fileExists(confPath)
	disabledExists := fileExists(disabledPath)
	userName := SiteUser(site)
	_, userErr := r.userLookup(userName)
	userExists := userErr == nil
	if !confExists && !disabledExists && len(poolPaths) == 0 && !userExists {
		return SiteResult{Site: site, Action: "delete"}, ErrNotFound
	}

	// Rollback copies BEFORE removal (criterion 3).
	keep := func(src string) {
		raw, err := readFileBounded(src, fileReadLimit)
		if err != nil {
			return
		}
		_ = os.WriteFile(src+RollbackSuffix, raw, 0o644)
		if strings.HasSuffix(src, ".conf") || strings.HasSuffix(src, DisabledSuffix) {
			res.RollbackPath = src + RollbackSuffix
		}
	}
	if confExists {
		keep(confPath)
		if err := os.Remove(confPath); err != nil {
			return res, err
		}
	}
	if disabledExists {
		keep(disabledPath)
		if err := os.Remove(disabledPath); err != nil {
			return res, err
		}
	}
	for _, p := range poolPaths {
		keep(p)
		if err := os.Remove(p); err != nil {
			return res, err
		}
	}
	if len(poolPaths) > 0 {
		res.PoolPath = poolPaths[0]
	}

	// Syntax gate + reload AFTER artifact removal; failure restores every
	// removed file from the rollback copies.
	if err := r.nginxAvailable(); err == nil {
		if out, err := r.run(ctx, r.nginxBinName(), "-t"); err != nil {
			restoreAll(confPath, disabledPath, poolPaths)
			return res, fmt.Errorf("%w: %s", ErrSyntaxCheck, clamp(out))
		}
		if _, err := r.run(ctx, r.systemctlName(), reloadVerb, "nginx"); err != nil {
			restoreAll(confPath, disabledPath, poolPaths)
			return res, fmt.Errorf("nginx reload sinyali başarısız: %w", err)
		}
	}

	// FPM reloads for each affected version (best-effort signals).
	seen := map[string]bool{}
	for _, p := range poolPaths {
		v := poolVersionOf(p)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		_, _ = r.run(ctx, r.systemctlName(), reloadVerb, PHPFPMUnit(v))
	}

	// User removal last; failure leaves an honest partial state (site is
	// already unserved) and the error names it.
	if userExists {
		if _, err := r.run(ctx, r.userdelBinName(), userName); err != nil {
			res.Notes = append(res.Notes, "config kaldırıldı ancak sistem kullanıcısı silinemedi (yeniden deneyin)")
			return res, fmt.Errorf("kullanıcı silinemedi: %w", err)
		}
	}
	res.User = userName
	if _, err := os.Stat(res.DocRoot); err == nil {
		res.DocPreserved = true
		res.Notes = append(res.Notes, "document root KORUNDU (kullanıcı verisi silinmez — ADR-0012)")
	}
	return res, nil
}

// restoreAll writes back removed artifacts (delete rollback path).
func restoreAll(confPath, disabledPath string, pools []string) {
	restore := func(p string) {
		rb := p + RollbackSuffix
		if raw, err := readFileBounded(rb, fileReadLimit); err == nil {
			_ = os.WriteFile(p, raw, 0o644)
		}
	}
	if confPath != "" {
		restore(confPath)
	}
	if disabledPath != "" {
		restore(disabledPath)
	}
	for _, p := range pools {
		restore(p)
	}
}

// poolVersionOf extracts the PHP version from a pool dir path
// (.../etc/php/<v>/fpm/pool.d/...).
func poolVersionOf(path string) string {
	parts := strings.Split(path, string(os.PathSeparator))
	for i, p := range parts {
		if p == "php" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---- read operations ----

// List implements web.site.list.
func (r *Runner) List(ctx context.Context, limit int) ([]SiteInfo, error) {
	if limit < 1 || limit > MaxSiteList {
		return nil, ErrLimit
	}
	confDir, webRoot, _ := r.paths()
	entries, err := os.ReadDir(confDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SiteInfo{}, nil
		}
		return nil, err
	}
	type row struct {
		info    SiteInfo
		enabled bool
	}
	byName := map[string]row{}
	names := []string{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, ConfPrefix) {
			continue
		}
		enabled, siteName := true, strings.TrimSuffix(strings.TrimSuffix(n, ".conf"), ConfPrefix)
		path := filepath.Join(confDir, n)
		if strings.HasSuffix(n, ".conf"+DisabledSuffix) {
			enabled, siteName = false, strings.TrimSuffix(n, ".conf"+DisabledSuffix)
			siteName = strings.TrimPrefix(siteName, ConfPrefix)
		} else if !strings.HasSuffix(n, ".conf") {
			continue
		}
		if !ValidSiteName(siteName) {
			continue // foreign file in our namespace shape — skip, never touch
		}
		raw, err := readFileBounded(path, fileReadLimit)
		if err != nil || !HasMarker(raw) {
			continue
		}
		name2, backend, php, port, sni := ParseSiteConfig(raw)
		if name2 != "" {
			siteName = name2
		}
		prev, dup := byName[siteName]
		if dup && (prev.enabled || enabled) {
			// Enabled form wins; a disabled duplicate is skipped when an
			// enabled row already exists.
			continue
		}
		if !dup {
			names = append(names, siteName)
		}
		info := SiteInfo{
			Name: siteName, Backend: backend, PHPVersion: php, Port: port,
			Enabled: enabled, User: SiteUser(siteName),
			DocRoot: filepath.Join(webRoot, siteName, "html"), ServerName: sni,
		}
		if _, err := r.userLookup(info.User); err == nil {
			info.UserExists = true
		}
		if _, err := os.Stat(info.DocRoot); err == nil {
			info.DocExists = true
		}
		byName[siteName] = row{info: info, enabled: enabled}
	}
	sort.Strings(names)
	out := make([]SiteInfo, 0, len(names))
	for _, n := range names {
		if len(out) >= limit {
			break
		}
		out = append(out, byName[n].info)
	}
	return out, nil
}

// Status implements web.site.status.
func (r *Runner) Status(ctx context.Context, site string) (SiteStatus, error) {
	if !ValidSiteName(site) {
		return SiteStatus{}, fmt.Errorf("%w: %q", ErrInvalidName, site)
	}
	confDir, webRoot, _ := r.paths()
	st := SiteStatus{}
	raw, err := readFileBounded(ConfFileName(confDir, site), fileReadLimit)
	switch {
	case err == nil:
		st.Site.Enabled = true
		st.ConfigPath = ConfFileName(confDir, site)
	case errors.Is(err, os.ErrNotExist):
		raw, err = readFileBounded(confDir+"/"+ConfPrefix+site+".conf"+DisabledSuffix, fileReadLimit)
		if err != nil {
			return st, ErrNotFound
		}
		st.ConfigPath = confDir + "/" + ConfPrefix + site + ".conf" + DisabledSuffix
	default:
		return st, err
	}
	if !HasMarker(raw) {
		return st, ErrForeignConfig
	}
	_, backend, php, port, sni := ParseSiteConfig(raw)
	st.Site = SiteInfo{
		Name: site, Backend: backend, PHPVersion: php, Port: port,
		Enabled: st.Site.Enabled, User: SiteUser(site),
		DocRoot: filepath.Join(webRoot, site, "html"), ServerName: sni,
	}
	if _, err := r.userLookup(st.Site.User); err == nil {
		st.Site.UserExists = true
	}
	if _, err := os.Stat(st.Site.DocRoot); err == nil {
		st.Site.DocExists = true
	}
	if rb, err := os.Stat(st.ConfigPath + RollbackSuffix); err == nil && !rb.IsDir() {
		st.RollbackPath = st.ConfigPath + RollbackSuffix
		st.HasRollback = true
	}
	for _, v := range PHPSupported {
		p := PHPPoolPath(r.poolDirFor(v), site)
		if fileExists(p) {
			st.PoolPath = p
			st.PoolInstalled = true
			break
		}
	}
	if ls, err := r.listeners(); err == nil {
		if port > 0 {
			verdict, owner := ClassifyPort(ls, port)
			switch verdict {
			case PortOwnedByNginx:
				st.Listener = fmt.Sprintf("port %d: nginx (yönetilen vhost)", port)
			case PortOwnedByApache:
				st.Listener = fmt.Sprintf("port %d: %s (Apache)", port, owner)
			case PortFree:
				st.Listener = fmt.Sprintf("port %d: dinleyici yok", port)
			default:
				st.Listener = fmt.Sprintf("port %d: %s", port, owner)
			}
		}
	}
	return st, nil
}

// PHPVersions implements web.php.list: closed matrix vs detected reality.
func (r *Runner) PHPVersions(ctx context.Context) (PHPMatrix, error) {
	m := PHPMatrix{Supported: append([]string(nil), PHPSupported...)}
	for _, v := range PHPSupported {
		if !r.phpInstalled(v) {
			continue
		}
		m.Installed = append(m.Installed, PHPState{
			Version: v, Unit: PHPFPMUnit(v), PoolDir: r.poolDirFor(v),
		})
	}
	for _, st := range m.Installed {
		entries, err := os.ReadDir(st.PoolDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasPrefix(n, ConfPrefix) || !strings.HasSuffix(n, ".conf") {
				continue
			}
			raw, err := readFileBounded(filepath.Join(st.PoolDir, n), fileReadLimit)
			if err != nil || !HasMarker(raw) {
				continue
			}
			site, _, _, _, _ := parsePoolMeta(raw)
			if site == "" {
				continue
			}
			m.Pools = append(m.Pools, PoolInfo{Site: site, Version: st.Version, Path: filepath.Join(st.PoolDir, n)})
		}
	}
	sort.Slice(m.Pools, func(i, j int) bool {
		if m.Pools[i].Site != m.Pools[j].Site {
			return m.Pools[i].Site < m.Pools[j].Site
		}
		return m.Pools[i].Version < m.Pools[j].Version
	})
	return m, nil
}

// parsePoolMeta reads the "; site: name" metadata of a managed pool file.
func parsePoolMeta(content []byte) (site, backend, php string, port int, names []string) {
	for _, line := range strings.Split(string(content), "\n") {
		t := strings.TrimSpace(line)
		t = strings.TrimSpace(strings.TrimPrefix(t, ";"))
		if strings.HasPrefix(t, metaSite) {
			site = strings.TrimSpace(strings.TrimPrefix(t, metaSite))
		}
	}
	return site, "", "", 0, nil
}
