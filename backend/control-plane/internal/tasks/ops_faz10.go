package tasks

// Faz 10: Veritabanı yönetimi + scoped dosya yöneticisi.
//
// Şemalar agent/internal/dbops ve agent/internal/fsops ile AYNA şeklinde
// tutulur (payload'lar iki tarafta da strict doğrulanır — ADR-0014):
//   - db.* ailesi YALNIZ node-local unix soketlerine dokunur; payload
//     şemasında host/port/DSN ALANI YOKTUR → panelin kendi PostgreSQL'i
//     yapısal olarak erişilemez (kriter 1). Korumalı adlar (sistem/panel)
//     broker + runner katmanlarında da reddedilir.
//   - db.user.create şifresi NODE'da üretilir; agentsrv ResultScrubber
//     sonucu kalıcılaştırmadan ÖNCE şifreyi seal edip ayırır (kriter 2 —
//     "password hiçbir zaman tekrar düz metin gösterilmemeli").
//   - db.backup/restore DB-native tutarlılıkla (pg_dump MVCC snapshot /
//     mysqldump --single-transaction) ve marker disiplinle çalışır
//     (kriter 3); restore yalnız marker'lı yönetilen dosyayı kabul eder.
//   - fs.* ailesinin kökleri YALNIZ webops site docroot'larıdır; yol
//     payload'dan ASLA kabul edilmez (kriter 5). Traversal/symlink/race/
//     boyut/MIME kapıları broker tarafında yarışsız openat ile uygulanır
//     (kriter 4).

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// Mirrors of the agent-side bounds (dbops/fsops).
const (
	MaxDBNameLen  = 63
	MaxDBHostLen  = 255
	MaxDBList     = 500
	MaxFSList     = 500
	MaxFSPathLen  = 400
	// MaxFSPayloadBytes: fs.upload gövde tavanı (base64 ≤ 768 KiB; broker
	// istek satırı tavanı 1 MiB ile hizalı — ADR-0014).
	MaxFSPayloadBytes = 768 << 10
)

// dbValidKind mirrors dbops.ValidKind.
func dbValidKind(kind string) bool { return kind == "postgres" || kind == "mysql" }

// dbValidName mirrors dbops.ValidName (strict identifier allowlist).
func dbValidName(name string) bool {
	if len(name) == 0 || len(name) > MaxDBNameLen {
		return false
	}
	for i, c := range name {
		ok := false
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			ok = true
		}
		if i == 0 && (c < 'a' || c > 'z') {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// dbValidHost mirrors dbops.ValidHost (MySQL host bileşeni; '%' tek başına).
func dbValidHost(host string) bool {
	if host == "%" {
		return true
	}
	if len(host) == 0 || len(host) > MaxDBHostLen {
		return false
	}
	for i, c := range host {
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-'
		if i == 0 && c == '-' {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// dbValidLevel mirrors dbops.ValidLevel (closed grant matrix).
func dbValidLevel(level string) bool {
	return level == "readonly" || level == "readwrite" || level == "all"
}

// dbValidBackup mirrors dbops.ValidBackupName (managed-dir filename).
func dbValidBackup(name string) bool {
	if len(name) == 0 || len(name) > 120 || name[len(name)-4:] != ".sql" {
		return false
	}
	c0 := name[0]
	if !(c0 >= 'A' && c0 <= 'Z' || c0 >= 'a' && c0 <= 'z' || c0 >= '0' && c0 <= '9') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// fsValidRoot mirrors fsops.ValidRoot → webops site adı allowlist'i.
func fsValidRoot(root string) bool { return webValidSiteName(root) }

// fsValidPath mirrors fsops.ValidRelPath (relative, no dot segments,
// bounded segments/bytes).
func fsValidPath(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > MaxFSPathLen {
		return errors.New("yol çok uzun")
	}
	if p[0] == '/' || p[len(p)-1] == '/' {
		return errors.New("yol mutlak veya sonda / olamaz")
	}
	segs := 0
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			seg := p[start:i]
			segs++
			if segs > 16 || len(seg) == 0 || len(seg) > 96 ||
				seg == "." || seg == ".." {
				return errors.New("yol segmenti geçersiz")
			}
			for _, c := range seg {
				ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
					c == '.' || c == '_' || c == '-'
				if !ok {
					return errors.New("yol segmenti karakter kümesi dışı")
				}
			}
			start = i + 1
		}
	}
	return nil
}

// fsValidMode mirrors fsops.ParseMode (octal string ≤ 0777).
func fsValidMode(mode string) bool {
	if mode == "" {
		return false
	}
	v := 0
	for i, c := range mode {
		if c < '0' || c > '7' || i >= 4 {
			return false
		}
		v = v*8 + int(c-'0')
	}
	return v >= 0 && v <= 0o777
}

// dbListPayload is the shared {kind, limit?} schema.
type dbListPayload struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit,omitempty"`
}

func validateDBList(payload json.RawMessage) error {
	var p dbListPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) {
		return errors.New("kind postgres|mysql olmalı")
	}
	if p.Limit < 0 || p.Limit > MaxDBList {
		return errors.New("limit 0..500 olmalı")
	}
	return nil
}

// dbDatabaseCreate is {kind, name, owner?}.
func validateDBDatabaseCreate(payload json.RawMessage) error {
	var p struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Owner string `json:"owner,omitempty"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) {
		return errors.New("kind postgres|mysql olmalı")
	}
	if !dbValidName(p.Name) {
		return errors.New("veritabanı adı allowlist dışı")
	}
	if p.Owner != "" && !dbValidName(p.Owner) {
		return errors.New("owner adı allowlist dışı")
	}
	return nil
}

// dbDatabasePayload is {kind, database} (delete/backup).
func validateDBDatabase(payload json.RawMessage) error {
	var p struct {
		Kind     string `json:"kind"`
		Database string `json:"database"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) || !dbValidName(p.Database) {
		return errors.New("kind/database geçersiz")
	}
	return nil
}

// dbUserPayload is {kind, name, host?} (create/delete). postgres host ALMAZ.
func validateDBUser(payload json.RawMessage) error {
	var p struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Host string `json:"host,omitempty"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) || !dbValidName(p.Name) {
		return errors.New("kind/name geçersiz")
	}
	if p.Kind == "mysql" {
		if p.Host == "" {
			p.Host = "localhost"
		}
		if !dbValidHost(p.Host) {
			return errors.New("host geçersiz")
		}
	} else if p.Host != "" {
		return errors.New("postgres user host almaz")
	}
	return nil
}

// dbGrantPayload is {kind, user, host?, database, level}.
func validateDBGrant(payload json.RawMessage) error {
	var p struct {
		Kind     string `json:"kind"`
		User     string `json:"user"`
		Host     string `json:"host,omitempty"`
		Database string `json:"database"`
		Level    string `json:"level"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) || !dbValidName(p.User) || !dbValidName(p.Database) {
		return errors.New("kind/user/database geçersiz")
	}
	if !dbValidLevel(p.Level) {
		return errors.New("level readonly|readwrite|all olmalı")
	}
	if p.Kind == "mysql" {
		if p.Host == "" {
			p.Host = "localhost"
		}
		if !dbValidHost(p.Host) {
			return errors.New("host geçersiz")
		}
	} else if p.Host != "" {
		return errors.New("postgres grant host almaz")
	}
	return nil
}

// dbRestorePayload is {kind, database, backup}.
func validateDBRestore(payload json.RawMessage) error {
	var p struct {
		Kind     string `json:"kind"`
		Database string `json:"database"`
		Backup   string `json:"backup"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dbValidKind(p.Kind) || !dbValidName(p.Database) {
		return errors.New("kind/database geçersiz")
	}
	if !dbValidBackup(p.Backup) {
		return errors.New("yedek adı allowlist dışı")
	}
	return nil
}

// fsRootPathPayload is {root, path} (download/chown).
func validateFSRootPath(payload json.RawMessage) error {
	var p struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !fsValidRoot(p.Root) {
		return errors.New("root site adı allowlist dışı")
	}
	if p.Path == "" {
		return errors.New("path zorunlu")
	}
	return fsValidPath(p.Path)
}

// fsUploadPayload is {root, path, content_b64, mode?, overwrite?}.
func validateFSUpload(payload json.RawMessage) error {
	var p struct {
		Root      string `json:"root"`
		Path      string `json:"path"`
		Content   string `json:"content_b64"`
		Mode      string `json:"mode,omitempty"`
		Overwrite bool   `json:"overwrite,omitempty"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !fsValidRoot(p.Root) {
		return errors.New("root site adı allowlist dışı")
	}
	if p.Path == "" {
		return errors.New("path zorunlu")
	}
	if err := fsValidPath(p.Path); err != nil {
		return err
	}
	if p.Mode != "" && !fsValidMode(p.Mode) {
		return errors.New("mode geçersiz (oktal ≤ 0777)")
	}
	return nil
}

// fsChmodPayload is {root, path, mode}.
func validateFSChmod(payload json.RawMessage) error {
	var p struct {
		Root string `json:"root"`
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !fsValidRoot(p.Root) {
		return errors.New("root site adı allowlist dışı")
	}
	if p.Path == "" {
		return errors.New("path zorunlu")
	}
	if err := fsValidPath(p.Path); err != nil {
		return err
	}
	if !fsValidMode(p.Mode) {
		return errors.New("mode geçersiz (oktal ≤ 0777)")
	}
	return nil
}

// buildFaz10Registry returns the Faz 10 operation specs (11 db + 5 fs).
func buildFaz10Registry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name: "db.instance.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate: func(p json.RawMessage) error {
				var v struct{}
				if len(p) == 0 {
					return nil
				}
				if err := decodeStrict(p, &v); err != nil {
					return fmt.Errorf("şema uyumsuz: %w", err)
				}
				return nil
			},
			RequiredPerm: rbac.PermDBRead,
		},
		{
			Name: "db.database.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBList,
			RequiredPerm: rbac.PermDBRead,
		},
		{
			Name: "db.database.create", Version: "1.0.0",
			// İstenen son durum "veritabanı var" → tekrar çalıştırma
			// yakınsar (zaten varsa no-op) → idempotent dürüsttür.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBDatabaseCreate,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.database.delete", Version: "1.0.0",
			// destructive: ikinci delete "bulunamadı" ile biter; retry-güvensiz.
			Class: ClassDestructive, Target: TargetNode, RetrySafe: false,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     validateDBDatabase,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.user.list", Version: "1.0.0",
			// Kullanıcı listesi şifre materyali TAŞIMAZ (yalnız ad/host).
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBList,
			RequiredPerm: rbac.PermDBRead,
		},
		{
			Name: "db.user.create", Version: "1.0.0",
			// Şifre node'da üretilir ve sonuçta TEK SEFERLİK döner; mevcut
			// kullanıcı → no-op (şifre DÖNDÜRÜLMEZ). Control-plane tarafında
			// agentsrv ResultScrubber şifreyi seal edip sonuçtan ayırır.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBUser,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.user.delete", Version: "1.0.0",
			Class: ClassDestructive, Target: TargetNode, RetrySafe: false,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBUser,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.grant.set", Version: "1.0.0",
			// desired-state: önce REVOKE sonra istenen seviye → tekrar yakınsar.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBGrant,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.backup", Version: "1.0.0",
			// Zaman damgalı yeni dosya üretir (desired-state değil) ama retry
			// zararsızdır (en fazla ikinci dosya); non_idempotent + retry-safe.
			Class: ClassNonIdem, Target: TargetNode, RetrySafe: true,
			Timeout: 5 * time.Minute, MaxPayload: 1 << 10,
			Validate:     validateDBDatabase,
			RequiredPerm: rbac.PermDBCtrl,
		},
		{
			Name: "db.backup.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     validateDBList,
			RequiredPerm: rbac.PermDBRead,
		},
		{
			Name: "db.restore", Version: "1.0.0",
			// destructive + retry-güvensiz: hedef veritabanının içeriğini
			// değiştirir; yarım restore operatör müdahalesi gerektirebilir
			// (belgelenmiş operasyon riski — ADR-0014).
			Class: ClassDestructive, Target: TargetNode, RetrySafe: false,
			Timeout: 5 * time.Minute, MaxPayload: 1 << 10,
			Validate:     validateDBRestore,
			RequiredPerm: rbac.PermDBCtrl,
		},

		// ---- Faz 10: scoped dosya yöneticisi ----
		{
			Name: "fs.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 4 << 10,
			Validate: func(p json.RawMessage) error {
				var v struct {
					Root  string `json:"root"`
					Path  string `json:"path,omitempty"`
					Limit int    `json:"limit,omitempty"`
				}
				if err := decodeStrict(p, &v); err != nil {
					return fmt.Errorf("şema uyumsuz: %w", err)
				}
				if !fsValidRoot(v.Root) {
					return errors.New("root site adı allowlist dışı")
				}
				if err := fsValidPath(v.Path); err != nil {
					return err
				}
				if v.Limit < 0 || v.Limit > MaxFSList {
					return errors.New("limit 0..500 olmalı")
				}
				return nil
			},
			RequiredPerm: rbac.PermFilesRead,
		},
		{
			Name: "fs.download", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 4 << 10,
			Validate:     validateFSRootPath,
			RequiredPerm: rbac.PermFilesRead,
		},
		{
			Name: "fs.upload", Version: "1.0.0",
			// İstenen son durum "bu baytlar bu yolda" → tekrar çalıştırma
			// (overwrite=true) yakınsar → idempotent + retry-safe.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: MaxFSPayloadBytes,
			Validate:     validateFSUpload,
			RequiredPerm: rbac.PermFilesCtrl,
		},
		{
			Name: "fs.chmod", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 4 << 10,
			Validate:     validateFSChmod,
			RequiredPerm: rbac.PermFilesCtrl,
		},
		{
			Name: "fs.chown", Version: "1.0.0",
			// Hedef kullanıcı payload'da YOK — site kullanıcısı türetilir;
			// tekrar çalıştırma aynı sonucu verir → idempotent.
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 4 << 10,
			Validate:     validateFSRootPath,
			RequiredPerm: rbac.PermFilesCtrl,
		},
	}
}
