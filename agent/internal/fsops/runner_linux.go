//go:build linux

package fsops

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
	"golang.org/x/sys/unix"
)

// Runner executes scoped file operations against site docroots. WebRoot is
// injectable for hermetic tests (default webops.DefaultWebRoot); SiteUserFn
// overrides the chown target resolution (tests inject the real file owner).
type Runner struct {
	WebRoot    string
	SiteUserFn func(site string) (uid, gid int, err error)
}

func (r *Runner) webRoot() string {
	if r.WebRoot != "" {
		return r.WebRoot
	}
	return webops.DefaultWebRoot
}

// siteRootDir returns the docroot path derived from the site name.
func (r *Runner) siteRootDir(site string) string {
	return webops.DocRoot(r.webRoot(), site)
}

// scopeProof verifies through /proc/self/fd that the OPEN fd's kernel-real
// path is still inside the canonical root (race guard: even if a site user
// swapped/renamed directories mid-walk, the opened object must remain in
// scope or the operation fails).
func scopeProof(fd int, canonicalRoot string) error {
	// /proc/self/fd readlink'i yoğun koşucularda tek seferlik transient
	// ENOENT/ESTALE verebiliyor (CI gözlemi, run 41532486447 — fsops
	// TestSecurityNegatives flake'i). Tek deneme; KANIT hâlâ kurulamıyorsa
	// fail-closed ErrScope — asla pas geçilmez. Altta yatan errno korunur
	// (teşhis edilebilirlik).
	var real string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		real, err = os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
		if err == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("%w: /proc/self/fd kanıtı okunamadı: %v", ErrScope, err)
	}
	if real == canonicalRoot || strings.HasPrefix(real, canonicalRoot+"/") {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrScope, "fd kök dışında")
}

// openRoot opens the canonical site docroot (O_DIRECTORY|O_NOFOLLOW) and
// returns (fd, canonicalPath). The docroot must exist (site created via
// web hosting) — a missing docroot is a typed refusal, never a guess.
func (r *Runner) openRoot(site string) (int, string, error) {
	if !ValidRoot(site) {
		return -1, "", ErrInvalidRoot
	}
	dir := r.siteRootDir(site)
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return -1, "", fmt.Errorf("%w: %s", ErrNotSiteRoot, site)
	}
	fd, err := unix.Openat(unix.AT_FDCWD, canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", fmt.Errorf("%w: %s", ErrNotSiteRoot, site)
	}
	if err := scopeProof(fd, canonical); err != nil {
		unix.Close(fd)
		return -1, "", err
	}
	return fd, canonical, nil
}

// walk opens each intermediate segment relative to the previous fd with
// O_NOFOLLOW (symlinked intermediates are typed refusals) and returns an fd
// pointing at the final PARENT directory plus the final segment name.
// When segs is empty the returned fd is a dup of rootFD (caller closes it:
// it never equals rootFD because of the dup).
func walk(rootFD int, segs []string) (int, string, error) {
	if len(segs) == 0 {
		dup, err := unix.Dup(rootFD)
		if err != nil {
			return -1, "", err
		}
		return dup, "", nil
	}
	cur := rootFD
	prev := int(-1) // last intermediate fd (owned here until handed over)
	for i := 0; i < len(segs)-1; i++ {
		fd, err := unix.Openat(cur, segs[i], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			if prev >= 0 {
				unix.Close(prev)
			}
			return -1, "", mapOpenErr(cur, segs[i], err)
		}
		if prev >= 0 {
			unix.Close(prev)
		}
		prev = fd
		cur = fd
	}
	return cur, segs[len(segs)-1], nil
}

// mapOpenErr types one openat failure. NOTE: with O_NOFOLLOW the kernel
// returns ENOTDIR (not ELOOP) when the final component is a symlink and
// O_DIRECTORY is requested — the Fstatat disambiguation keeps the typed
// refusal honest (symlink ≠ missing ≠ invalid path).
func mapOpenErr(dirFD int, name string, err error) error {
	switch {
	case err == unix.ELOOP:
		return ErrSymlink
	case err == unix.ENOTDIR:
		var st unix.Stat_t
		if ferr := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); ferr == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return ErrSymlink
		}
		return ErrInvalidPath
	case err == unix.ENOENT:
		return ErrNotFound
	case err == unix.EEXIST:
		return ErrExists
	}
	return fmt.Errorf("aç: %w", err)
}

// walkFinal opens the final component with the given flags+mode using
// O_NOFOLLOW, relative to parentFD, then runs the scope proof.
func walkFinal(parentFD int, name string, canonicalRoot string, flags int, mode uint32) (int, error) {
	fd, err := unix.Openat(parentFD, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return -1, mapOpenErr(parentFD, name, err)
	}
	if err := scopeProof(fd, canonicalRoot); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// Entry is one listing row.
type Entry struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // file|dir|symlink|other
	Size    int64  `json:"size"`
	Mode    string `json:"mode"` // octal, e.g. "0644"
	ModTime string `json:"mod_time"`
	Link    string `json:"link,omitempty"` // symlink target (display only)
}

// fstatEntry builds an Entry from an Fstatat result.
func fstatEntry(name string, st *unix.Stat_t, link string) Entry {
	e := Entry{Name: name, Size: st.Size, Link: link}
	mode := st.Mode & unix.S_IFMT
	switch mode {
	case unix.S_IFREG:
		e.Type = "file"
	case unix.S_IFDIR:
		e.Type = "dir"
	case unix.S_IFLNK:
		e.Type = "symlink"
	default:
		e.Type = "other"
	}
	e.Mode = "0" + strconv.FormatUint(uint64(st.Mode&0o777), 8)
	e.ModTime = time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC().Format(time.RFC3339)
	return e
}

// ListResult is the fs.list task result.
type ListResult struct {
	Root      string  `json:"root"`
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// List lists one directory inside the site scope (bounded: Readdirnames
// with limit+1, no unbounded memory).
func (r *Runner) List(ctx context.Context, root, path string, limit int) (ListResult, error) {
	res := ListResult{Root: root, Path: path}
	if limit < 0 || limit > MaxListEntries {
		return res, ErrTooManyEntries
	}
	if limit == 0 {
		limit = 200
	}
	segs, err := ValidRelPath(path)
	if err != nil {
		return res, err
	}
	rootFD, canonical, err := r.openRoot(root)
	if err != nil {
		return res, err
	}
	defer unix.Close(rootFD)
	parentFD, final, err := walk(rootFD, segs)
	if err != nil {
		return res, err
	}

	// CI-33 (CI -race gerçek koşusu): fd sahipliği tek yerde. final=="" →
	// parentFD zaten rootFD'nin dup'idır ve dirFD olarak tek kez
	// kapatılır; final!="" → parentFD rootFD DEĞİLSE (ara dizin) ayrıca
	// kapatılır, dirFD walkFinal'ın sahibidir. Çift kapanış, fd-reuse ile
	// ilgisiz bir fd'yi kapatabilir (EBADF) — CI'da -race altında patladı.
	var dirFD int
	switch {
	case final == "":
		dirFD = parentFD
	case parentFD != rootFD:
		defer unix.Close(parentFD)
		fd, ferr := walkFinal(parentFD, final, canonical, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if ferr != nil {
			return res, ferr
		}
		dirFD = fd
	default:
		fd, ferr := walkFinal(parentFD, final, canonical, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if ferr != nil {
			return res, ferr
		}
		dirFD = fd
	}
	defer unix.Close(dirFD)
	if err := scopeProof(dirFD, canonical); err != nil {
		return res, err
	}

	// os.NewFile does NOT dup — it wraps the fd; Close must never be called
	// on it (dirFD has its own defer above).
	f := os.NewFile(uintptr(dirFD), "")
	namesRaw, rerr := f.Readdirnames(limit + 1)
	if rerr != nil {
		return res, fmt.Errorf("dizin okuma: %w", rerr)
	}
	if len(namesRaw) > limit {
		res.Truncated = true
		namesRaw = namesRaw[:limit]
	}
	sort.Strings(namesRaw)
	for _, name := range namesRaw {
		var st unix.Stat_t
		if err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue // raced away — skip honestly
		}
		link := ""
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			buf := make([]byte, 400)
			n, err := unix.Readlinkat(dirFD, name, buf)
			if err == nil {
				link = string(buf[:n])
			}
		}
		res.Entries = append(res.Entries, fstatEntry(name, &st, link))
	}
	return res, nil
}

// DownloadResult is the fs.download task result (content base64).
type DownloadResult struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	SHA256    string `json:"sha256"`
	Mime      string `json:"mime"`
	Content   string `json:"content_b64"`
	Truncated bool   `json:"truncated"`
}

// Download reads ONE file inside the scope (regular files only, ≤ cap).
func (r *Runner) Download(ctx context.Context, root, path string) (DownloadResult, error) {
	res := DownloadResult{Root: root, Path: path}
	segs, err := ValidRelPath(path)
	if err != nil {
		return res, err
	}
	if len(segs) == 0 {
		return res, ErrInvalidPath // a docroot is not downloadable
	}
	rootFD, canonical, err := r.openRoot(root)
	if err != nil {
		return res, err
	}
	defer unix.Close(rootFD)
	parentFD, final, err := walk(rootFD, segs)
	if err != nil {
		return res, err
	}
	if parentFD != rootFD {
		defer unix.Close(parentFD)
	}
	fd, err := walkFinal(parentFD, final, canonical, unix.O_RDONLY, 0)
	if err != nil {
		return res, err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return res, fmt.Errorf("stat: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return res, ErrInvalidPath // directories/special files are not downloadable
	}
	if st.Size > RawFileCap {
		return res, ErrTooLarge
	}
	buf := make([]byte, st.Size)
	if _, err := readFull(fd, buf); err != nil {
		return res, fmt.Errorf("oku: %w", err)
	}
	sum := sha256.Sum256(buf)
	res.Size = int64(len(buf))
	res.Mode = "0" + strconv.FormatUint(uint64(st.Mode&0o777), 8)
	res.SHA256 = hex.EncodeToString(sum[:])
	res.Mime = http.DetectContentType(mimeSample(buf))
	res.Content = base64.StdEncoding.EncodeToString(buf)
	return res, nil
}

func mimeSample(b []byte) []byte {
	if len(b) > 512 {
		return b[:512]
	}
	return b
}

func readFull(fd int, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := unix.Read(fd, buf[total:])
		if n > 0 {
			total += n
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
	}
	return total, nil
}

// UploadResult is the fs.upload task result.
type UploadResult struct {
	Root        string `json:"root"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	Mode        string `json:"mode"`
	Mime        string `json:"mime"`
	Created     bool   `json:"created"`
	Overwritten bool   `json:"overwritten"`
}

// Upload writes ONE file inside the scope. Policy order: path validation →
// scope → size cap → extension/MIME policy → write (O_EXCL create or
// temp+rename overwrite) → scope proof. Partial creates are cleaned up.
func (r *Runner) Upload(ctx context.Context, root, path, contentB64 string, mode uint32, overwrite bool) (UploadResult, error) {
	res := UploadResult{Root: root, Path: path}
	segs, err := ValidRelPath(path)
	if err != nil {
		return res, err
	}
	if len(segs) == 0 {
		return res, ErrInvalidPath
	}
	content, err := DecodeContent(contentB64)
	if err != nil {
		return res, err
	}
	mime, perr := CheckUploadPolicy(segs[len(segs)-1], content)
	if perr != nil {
		res.Mime = mime
		return res, perr
	}
	res.Mime = mime
	if mode > MaxMode {
		return res, ErrInvalidMode
	}
	if mode == 0 {
		mode = 0o644
	}
	rootFD, canonical, err := r.openRoot(root)
	if err != nil {
		return res, err
	}
	defer unix.Close(rootFD)
	parentFD, final, err := walk(rootFD, segs)
	if err != nil {
		return res, err
	}
	if parentFD != rootFD {
		defer unix.Close(parentFD)
	}

	if !overwrite {
		fd, err := walkFinal(parentFD, final, canonical, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, mode)
		if err != nil {
			return res, err
		}
		if _, werr := writeFull(fd, content); werr != nil {
			unix.Close(fd)
			_ = unix.Unlinkat(parentFD, final, 0)
			return res, fmt.Errorf("yaz: %w", werr)
		}
		_ = unix.Fsync(fd)
		unix.Close(fd)
		res.Bytes = int64(len(content))
		res.Mode = "0" + strconv.FormatUint(uint64(mode), 8)
		res.Created = true
		return res, nil
	}

	// Overwrite: temp file (same dir, random suffix) → write → renameat.
	// renameat within the same parent CANNOT escape the scope; the temp fd
	// still gets the /proc scope proof before the rename. A symlink at the
	// target is NEVER replaced (typed refusal — no silent link destruction).
	var tst unix.Stat_t
	if err := unix.Fstatat(parentFD, final, &tst, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if tst.Mode&unix.S_IFMT == unix.S_IFLNK {
			return res, ErrSymlink
		}
	}
	tmpName := fmt.Sprintf(".srvctl-upload-%d-%d.tmp", os.Getpid(), time.Now().UnixNano())
	tmpFD, err := unix.Openat(parentFD, tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return res, fmt.Errorf("temp: %w", err)
	}
	if _, werr := writeFull(tmpFD, content); werr != nil {
		unix.Close(tmpFD)
		_ = unix.Unlinkat(parentFD, tmpName, 0)
		return res, fmt.Errorf("yaz: %w", werr)
	}
	_ = unix.Fsync(tmpFD)
	if err := scopeProof(tmpFD, canonical); err != nil {
		unix.Close(tmpFD)
		_ = unix.Unlinkat(parentFD, tmpName, 0)
		return res, err
	}
	unix.Close(tmpFD)
	if err := unix.Renameat(parentFD, tmpName, parentFD, final); err != nil {
		_ = unix.Unlinkat(parentFD, tmpName, 0)
		return res, fmt.Errorf("rename: %w", err)
	}
	res.Bytes = int64(len(content))
	res.Mode = "0" + strconv.FormatUint(uint64(mode), 8)
	res.Overwritten = true
	return res, nil
}

func writeFull(fd int, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := unix.Write(fd, buf[total:])
		if n > 0 {
			total += n
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ChmodResult is the fs.chmod task result.
type ChmodResult struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	OldMode string `json:"old_mode"`
	NewMode string `json:"new_mode"`
	Type    string `json:"type"`
}

// Chmod changes the mode of one in-scope entry via an O_NOFOLLOW-opened fd
// (race-free; symlink targets are never followed).
func (r *Runner) Chmod(ctx context.Context, root, path string, mode uint32) (ChmodResult, error) {
	res := ChmodResult{Root: root, Path: path}
	if mode > MaxMode {
		return res, ErrInvalidMode
	}
	segs, err := ValidRelPath(path)
	if err != nil {
		return res, err
	}
	if len(segs) == 0 {
		return res, ErrInvalidPath
	}
	rootFD, canonical, err := r.openRoot(root)
	if err != nil {
		return res, err
	}
	defer unix.Close(rootFD)
	parentFD, final, err := walk(rootFD, segs)
	if err != nil {
		return res, err
	}
	if parentFD != rootFD {
		defer unix.Close(parentFD)
	}
	fd, err := walkFinal(parentFD, final, canonical, unix.O_RDONLY, 0)
	if err != nil {
		return res, err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return res, fmt.Errorf("stat: %w", err)
	}
	res.OldMode = "0" + strconv.FormatUint(uint64(st.Mode&0o777), 8)
	if err := unix.Fchmod(fd, mode); err != nil {
		return res, fmt.Errorf("chmod: %w", err)
	}
	res.NewMode = "0" + strconv.FormatUint(uint64(mode), 8)
	res.Type = fstatEntry(final, &st, "").Type
	return res, nil
}

// ChownResult is the fs.chown task result.
type ChownResult struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	User   string `json:"user"`
	OldUID int    `json:"old_uid"`
	OldGID int    `json:"old_gid"`
}

// Chown restores the ownership of the site's OWN system user (derived from
// the root name — the payload has NO uid/gid/target fields).
func (r *Runner) Chown(ctx context.Context, root, path string) (ChownResult, error) {
	res := ChownResult{Root: root, Path: path}
	segs, err := ValidRelPath(path)
	if err != nil {
		return res, err
	}
	if len(segs) == 0 {
		return res, ErrInvalidPath
	}
	uid, gid, err := r.resolveSiteUser(root)
	if err != nil {
		return res, err
	}
	rootFD, canonical, err := r.openRoot(root)
	if err != nil {
		return res, err
	}
	defer unix.Close(rootFD)
	parentFD, final, err := walk(rootFD, segs)
	if err != nil {
		return res, err
	}
	if parentFD != rootFD {
		defer unix.Close(parentFD)
	}
	fd, err := walkFinal(parentFD, final, canonical, unix.O_RDONLY, 0)
	if err != nil {
		return res, err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return res, fmt.Errorf("stat: %w", err)
	}
	res.OldUID, res.OldGID = int(st.Uid), int(st.Gid)
	if err := unix.Fchown(fd, uid, gid); err != nil {
		return res, fmt.Errorf("chown: %w", err)
	}
	res.UID, res.GID, res.User = uid, gid, webops.SiteUser(root)
	return res, nil
}

// resolveSiteUser looks up the derived site user (injectable for tests).
func (r *Runner) resolveSiteUser(site string) (int, int, error) {
	if r.SiteUserFn != nil {
		return r.SiteUserFn(site)
	}
	u, err := user.Lookup(webops.SiteUser(site))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %s", ErrNoSiteUser, webops.SiteUser(site))
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: uid", ErrNoSiteUser)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: gid", ErrNoSiteUser)
	}
	return uid, gid, nil
}
