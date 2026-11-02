package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db/migrate"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RestoreDrill rehearses the restore of one Control Plane backup into an
// ISOLATED database (kriter 2): a freshly created scp_drill_xxxxxxxx database
// receives the embedded migrations (schema) and then the artifact's COPY
// data; row counts are compared per table. The live database is never
// touched; the drill database is dropped afterwards (best-effort, honestly
// reported on failure).
func (s *Service) RestoreDrill(ctx context.Context, pool *pgxpool.Pool, backupID, actor string) (Drill, error) {
	if err := ValidateBackupID(backupID); err != nil {
		return Drill{}, err
	}
	rec, err := s.Store.GetBackup(ctx, backupID)
	if err != nil {
		return Drill{}, err
	}
	if rec.Scope != ScopeControlPlane {
		return Drill{}, fmt.Errorf("%w: kontrol paneli yedeği değil", ErrInvalidState)
	}
	if rec.Status != StatusSuccess {
		return Drill{}, fmt.Errorf("%w: yedek durumu %s (yalnız success tatbikat edilebilir)", ErrInvalidState, rec.Status)
	}
	if rec.StoragePath == "" {
		return Drill{}, fmt.Errorf("%w: yedek dosya yolu yok", ErrCorruptBackup)
	}

	live := liveDBName(s.DatabaseURL)
	if live == "" {
		return Drill{}, fmt.Errorf("backup: canlı veritabanı adı çözümlenemedi")
	}
	drillDB := drillDBPrefix + randHex8()
	if err := validateDrillDBName(drillDB, live); err != nil {
		return Drill{}, err
	}

	drill, err := s.Store.InsertDrill(ctx, backupID, "", drillDB, actor, "")
	if err != nil {
		return Drill{}, err
	}

	tables, rows, detail, derr := s.runDrill(ctx, pool, rec.StoragePath, drillDB)
	if derr != nil {
		cleanup := s.dropDrillDB(ctx, pool, drillDB)
		full := truncate(derr.Error(), 900)
		if cleanup != "" {
			full = truncate(full+"; temizlik: "+cleanup, 1000)
		}
		return s.Store.CompleteDrill(ctx, drill.ID, StatusFailed, full, tables, rows)
	}
	cleanup := s.dropDrillDB(ctx, pool, drillDB)
	detail = truncate(detail, 900)
	if cleanup != "" {
		detail = truncate(detail+"; temizlik uyarısı: "+cleanup, 1000)
	}
	return s.Store.CompleteDrill(ctx, drill.ID, StatusSuccess, detail, tables, rows)
}

// runDrill creates the isolated database, replays migrations + data and
// verifies row counts. Returns restored table/row counts.
func (s *Service) runDrill(ctx context.Context, pool *pgxpool.Pool, artifactPath, drillDB string) (int, int64, string, error) {
	// 1) CREATE DATABASE on the live server (requires CREATEDB privilege).
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, quoteIdent(drillDB))); err != nil {
		if isPgCode(err, "42501", "42510") {
			return 0, 0, "", fmt.Errorf("%w: %v", ErrDatabaseDenied, pgErr(err))
		}
		if isPgCode(err, "42P04") {
			return 0, 0, "", fmt.Errorf("%w: hedef adı çakışıyor: %v", ErrUnsafeTarget, pgErr(err))
		}
		return 0, 0, "", fmt.Errorf("backup: tatbikat veritabanı: %w", pgErr(err))
	}

	// 2) Connect to the isolated database (own pool; closed before DROP).
	durl, err := drillURL(s.DatabaseURL, drillDB)
	if err != nil {
		return 0, 0, "", err
	}
	dpool, err := pgxpool.New(ctx, durl)
	if err != nil {
		return 0, 0, "", fmt.Errorf("backup: tatbikat havuzu: %w", err)
	}
	defer dpool.Close()

	// 3) Schema: replay the embedded migrations (the CP schema's single
	// source of truth) — no hand-maintained DDL duplication.
	runner := &migrate.Runner{Pool: dpool, Migrations: s.Migrations}
	if _, err := runner.Up(ctx); err != nil {
		return 0, 0, "", fmt.Errorf("backup: tatbikat şeması: %w", err)
	}

	// 3b) Migrations seed some tables; the ARTIFACT is the authoritative
	// snapshot. Truncate every public table (CASCADE, transactional guards
	// not needed — the drill db is disposable) so the COPY replay never
	// collides with seed rows.
	if err := truncateAllPublic(ctx, dpool); err != nil {
		return 0, 0, "", err
	}

	// 4) Data: stream artifact sections with COPY FROM STDIN (topo order).
	f, err := openArtifact(artifactPath)
	if err != nil {
		return 0, 0, "", err
	}
	defer f.Close()
	sections, err := loadSections(ctx, dpool, bufio.NewReaderSize(f, 256<<10))
	if err != nil {
		return 0, 0, "", err
	}

	// 5) Sequences: serial counters must resume past the restored ids.
	if err := fixSequences(ctx, dpool); err != nil {
		return 0, 0, "", err
	}

	// 6) Verify: artifact counts vs restored counts (row-for-row).
	tables := len(sections)
	var rows int64
	for name, n := range sections {
		var got int64
		if err := dpool.QueryRow(ctx, "SELECT count(*) FROM "+name).Scan(&got); err != nil {
			return tables, rows, "", fmt.Errorf("backup: tatbikat sayımı %s: %w", name, pgErr(err))
		}
		if got != n {
			return tables, rows, "", fmt.Errorf("%w: %s satır sayısı %d ≠ %d", ErrCorruptBackup, name, got, n)
		}
		rows += n
	}
	summary := fmt.Sprintf("hedef=%s tablo=%d satır=%d; tatbikat veritabanı düşürüldü", drillDB, tables, rows)
	return tables, rows, summary, nil
}

// truncateAllPublic empties every public base table in the drill database
// (migrations seed rows; the artifact is authoritative). Names come from the
// catalog of the DISPOSABLE drill db and are shape-validated before use.
func truncateAllPublic(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
SELECT c.relname FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname = 'public' AND c.relname <> 'schema_migrations'`)
	if err != nil {
		return fmt.Errorf("backup: tatbikat tablo listesi: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !identShapeRE.MatchString(name) {
			return fmt.Errorf("backup: beklenmeyen tablo adı: %q", name)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "public."+quoteIdent(n))
	}
	if _, err := pool.Exec(ctx, "TRUNCATE "+strings.Join(quoted, ", ")+" CASCADE"); err != nil {
		return fmt.Errorf("backup: tatbikat truncate: %w", pgErr(err))
	}
	return nil
}

// sectionLineReader serves exactly n COPY text lines then io.EOF. Data lines
// pass through unmodified; the parser consumed the section header already.
type sectionLineReader struct {
	br        *bufio.Reader
	remaining int64
	buf       []byte
	eof       bool
}

func (s *sectionLineReader) Read(p []byte) (int, error) {
	if s.eof {
		return 0, io.EOF
	}
	if len(s.buf) == 0 {
		if s.remaining <= 0 {
			s.eof = true
			return 0, io.EOF
		}
		line, err := s.br.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			s.eof = true
			return 0, err
		}
		if len(line) > maxDataLine {
			s.eof = true
			return 0, fmt.Errorf("%w: aşırı veri satırı", ErrCorruptBackup)
		}
		s.buf = line
		s.remaining--
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// loadSections parses the artifact and replays every table section into the
// drill database with COPY FROM STDIN. Returns table→row-count map.
func loadSections(ctx context.Context, pool *pgxpool.Pool, br *bufio.Reader) (map[string]int64, error) {
	out := map[string]int64{}

	readLine := func() ([]byte, error) { return br.ReadBytes('\n') }

	first, err := readLine()
	if err != nil || strings.TrimRight(string(first), "\n") != FormatMarker {
		return nil, ErrCorruptBackup
	}
	for {
		line, err := readLine()
		if err != nil {
			return nil, ErrCorruptBackup
		}
		s := strings.TrimRight(string(line), "\n")
		if s == "" {
			break
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: tatbikat işlemi: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for {
		line, err := readLine()
		if err != nil {
			return nil, ErrCorruptBackup
		}
		s := strings.TrimRight(string(line), "\n")
		switch {
		case strings.HasPrefix(s, "TABLE "):
			name, nRows, ok := parseTableHeader(s)
			if !ok {
				return nil, ErrCorruptBackup
			}
			// Catalog-quoted names from our own dump: re-validated.
			q := quoteQualifiedName(name)
			if q == "" {
				return nil, fmt.Errorf("%w: tablo adı geçersiz: %q", ErrCorruptBackup, name)
			}
			r := &sectionLineReader{br: br, remaining: nRows}
			if _, err := tx.Conn().PgConn().CopyFrom(ctx, r, "COPY "+q+" FROM STDIN"); err != nil {
				return nil, fmt.Errorf("backup: tatbikat COPY %s: %w", q, pgErr(err))
			}
			// Section trailer: ENDTABLE (verified, then skipped).
			end, eerr := readLine()
			if eerr != nil || strings.TrimRight(string(end), "\n") != "ENDTABLE" {
				return nil, fmt.Errorf("%w: tatbikat ENDTABLE bekleniyordu (%s)", ErrCorruptBackup, name)
			}
			out[name] = nRows
		case strings.HasPrefix(s, "FOOTER "):
			if _, _, _, ok := parseFooter(s); !ok {
				return nil, fmt.Errorf("%w: footer bozuk", ErrCorruptBackup)
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("backup: tatbikat commit: %w", err)
			}
			return out, nil
		default:
			return nil, fmt.Errorf("%w: tatbikat beklenmeyen satır: %q", ErrCorruptBackup, truncate(s, 32))
		}
	}
}

// quoteQualifiedName validates a quoted "schema"."table" pair from the
// artifact. Anything outside the strict quoted shape is rejected (the drill
// never trusts artifact strings for SQL composition).
func quoteQualifiedName(name string) string {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	for _, p := range parts {
		if len(p) < 2 || !strings.HasPrefix(p, `"`) || !strings.HasSuffix(p, `"`) {
			return ""
		}
		inner := p[1 : len(p)-1]
		if strings.Contains(inner, `"`) || !identShapeRE.MatchString(inner) {
			return ""
		}
	}
	return parts[0] + "." + parts[1]
}

// fixSequences advances every serial sequence past the restored ids so new
// inserts after a real restore never collide.
func fixSequences(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
SELECT table_schema, table_name, column_name
FROM information_schema.columns
WHERE table_schema = 'public' AND column_default LIKE 'nextval(%'`)
	if err != nil {
		return fmt.Errorf("backup: sequence taraması: %w", err)
	}
	defer rows.Close()
	type seqRef struct{ schema, table, column string }
	var seqs []seqRef
	for rows.Next() {
		var r seqRef
		if err := rows.Scan(&r.schema, &r.table, &r.column); err != nil {
			return err
		}
		if !identShapeRE.MatchString(r.schema) || !identShapeRE.MatchString(r.table) || !identShapeRE.MatchString(r.column) {
			return fmt.Errorf("backup: beklenmeyen sequence biçimi: %s.%s.%s", r.schema, r.table, r.column)
		}
		seqs = append(seqs, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range seqs {
		tbl := quoteIdent(r.schema) + "." + quoteIdent(r.table)
		col := quoteIdent(r.column)
		sql := fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s.%s', '%s'), COALESCE((SELECT max(%s) FROM %s), 1), (SELECT max(%s) FROM %s) IS NOT NULL)`,
			r.schema, r.table, r.column, col, tbl, col, tbl)
		if _, err := pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("backup: sequence %s.%s: %w", r.table, r.column, pgErr(err))
		}
	}
	return nil
}

// dropDrillDB terminates connections and drops the isolated drill database.
// Best-effort: the returned string is empty on success, otherwise an honest
// warning recorded in the drill detail.
func (s *Service) dropDrillDB(ctx context.Context, pool *pgxpool.Pool, drillDB string) string {
	dctx, cancel := context.WithTimeout(context.Background(), drillCleanupTimeout)
	defer cancel()
	if _, err := pool.Exec(dctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
		drillDB); err != nil {
		return "bağlantılar kapatılamadı: " + truncate(pgErr(err).Error(), 120)
	}
	if _, err := pool.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, quoteIdent(drillDB))); err != nil {
		return "DROP DATABASE başarısız: " + truncate(pgErr(err).Error(), 120)
	}
	return ""
}

// drillCleanupTimeout bounds the post-drill cleanup.
const drillCleanupTimeout = 30 * time.Second

// isPgCode reports whether the error carries one of the given SQLSTATE codes.
func isPgCode(err error, codes ...string) bool {
	var pge *pgconn.PgError
	if errors.As(err, &pge) {
		for _, c := range codes {
			if pge.Code == c {
				return true
			}
		}
	}
	return false
}

// openArtifact opens the artifact file (path comes from our own store; the
// verify pass re-validates content anyway).
func openArtifact(path string) (*os.File, error) {
	if !strings.HasSuffix(path, ".scpbackup") {
		return nil, fmt.Errorf("%w: yedek dosya uzantısı geçersiz", ErrCorruptBackup)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backup: yedek dosyası: %w", err)
	}
	return f, nil
}
