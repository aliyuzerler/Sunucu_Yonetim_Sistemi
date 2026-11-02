package backup

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tableRef is one qualified table in the dump manifest.
type tableRef struct {
	Schema string
	Name   string
}

func (t tableRef) qualified() string {
	return quoteIdent(t.Schema) + "." + quoteIdent(t.Name)
}

// quoteIdent wraps an identifier in double quotes with doubling escapes
// (catalog-derived names, but quoted defensively anyway).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

var identShapeRE = regexp.MustCompile(`^[a-z_][a-z0-9_$]*$`)

// rowsFieldWidth is the fixed-width, zero-padded row counter inside every
// TABLE header. A fixed width makes the field patchable in place (the value
// is only known after the table's COPY stream completes).
const rowsFieldWidth = 16

// listTables reads the BASE TABLE manifest of the public schema together with
// foreign-key edges, and returns the tables in topological order (parents
// first) so restore can replay data without constraint violations. Cycles
// fall back to deterministic order and the drill reports honestly.
func listTables(ctx context.Context, conn *pgx.Conn) ([]tableRef, error) {
	rows, err := conn.Query(ctx, `
SELECT n.nspname, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname = 'public'
  AND c.relname <> 'schema_migrations'
ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("backup: tablo manifesti: %w", err)
	}
	defer rows.Close()
	var tables []tableRef
	for rows.Next() {
		var t tableRef
		if err := rows.Scan(&t.Schema, &t.Name); err != nil {
			return nil, err
		}
		if !identShapeRE.MatchString(t.Schema) || !identShapeRE.MatchString(t.Name) {
			return nil, fmt.Errorf("backup: beklenmeyen tablo adı biçimi: %s.%s", t.Schema, t.Name)
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("backup: public şemasında tablo yok")
	}

	// FK edges child→parent (from information_schema, catalog-derived only).
	edges := map[string][]string{}
	rows2, err := conn.Query(ctx, `
SELECT DISTINCT ku.table_schema, ku.table_name, ccu.table_schema, ccu.table_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage ku
  ON ku.constraint_name = tc.constraint_name AND ku.table_schema = tc.table_schema
JOIN information_schema.constraint_column_usage ccu
  ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("backup: fk grafiği: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var cs, ct, ps, pt string
		if err := rows2.Scan(&cs, &ct, &ps, &pt); err != nil {
			return nil, err
		}
		child := cs + "." + ct
		parent := ps + "." + pt
		if child != parent {
			edges[child] = append(edges[child], parent)
		}
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}
	return topoSort(tables, edges), nil
}

// topoSort orders tables parents-first (Kahn). Independent tables keep
// alphabetical order for deterministic dumps.
func topoSort(tables []tableRef, edges map[string][]string) []tableRef {
	key := func(t tableRef) string { return t.Schema + "." + t.Name }
	byKey := map[string]tableRef{}
	indeg := map[string]int{}
	for _, t := range tables {
		byKey[key(t)] = t
		indeg[key(t)] = 0
	}
	for child, parents := range edges {
		if _, ok := byKey[child]; !ok {
			continue
		}
		for _, p := range parents {
			if _, ok := byKey[p]; ok {
				indeg[child]++
			}
		}
	}
	var ready []string
	for _, t := range tables {
		if indeg[key(t)] == 0 {
			ready = append(ready, key(t))
		}
	}
	sort.Strings(ready)
	out := make([]tableRef, 0, len(tables))
	for len(ready) > 0 {
		k := ready[0]
		ready = ready[1:]
		out = append(out, byKey[k])
		for child, parents := range edges {
			if _, ok := byKey[child]; !ok {
				continue
			}
			for _, p := range parents {
				if p == k {
					indeg[child]--
					if indeg[child] == 0 {
						ready = append(ready, child)
					}
				}
			}
		}
		sort.Strings(ready)
	}
	if len(out) != len(tables) {
		out = append(out, tables...)
		seen := map[string]bool{}
		dedup := out[:0]
		for _, t := range out {
			if !seen[key(t)] {
				seen[key(t)] = true
				dedup = append(dedup, t)
			}
		}
		out = dedup
	}
	return out
}

// countWriter counts bytes+lines written and enforces the size cap.
type countWriter struct {
	w     io.Writer
	bytes int64
	lines int64
	limit int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	if c.limit > 0 && c.bytes+int64(len(p)) > c.limit {
		return 0, fmt.Errorf("%w: %d bayt > %d sınırı", ErrBackupLimit, c.bytes+int64(len(p)), c.limit)
	}
	n, err := c.w.Write(p)
	c.bytes += int64(n)
	for _, b := range p[:n] {
		if b == '\n' {
			c.lines++
		}
	}
	return n, err
}

// cappedWriter enforces the size cap without counting lines.
type cappedWriter struct {
	w     io.Writer
	bytes int64
	limit int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.limit > 0 && c.bytes+int64(len(p)) > c.limit {
		return 0, fmt.Errorf("%w: %d bayt > %d sınırı", ErrBackupLimit, c.bytes+int64(len(p)), c.limit)
	}
	n, err := c.w.Write(p)
	c.bytes += int64(n)
	return n, err
}

// Dump performs the Control Plane backup: a single REPEATABLE READ
// transaction streams every public table with COPY TO STDOUT into the
// artifact (marker header, row-counted table sections, sha256 footer).
// The artifact is written to BackupDir/control-plane-<ts>-<rand>.scpbackup
// and the on-disk result is re-verified before success is recorded.
func (s *Service) Dump(ctx context.Context, pool *pgxpool.Pool) (Record, error) {
	if err := os.MkdirAll(s.BackupDir, 0o700); err != nil {
		return Record{}, fmt.Errorf("backup: yedek dizini: %w", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("backup: bağlantı: %w", err)
	}
	defer conn.Release()

	tables, err := listTables(ctx, conn.Conn())
	if err != nil {
		return Record{}, err
	}

	name := fmt.Sprintf("control-plane-%s-%08x.scpbackup",
		time.Now().UTC().Format("20060102T150405Z"), randHex8())
	path := filepath.Join(s.BackupDir, name)

	rec := Record{
		Scope:  ScopeControlPlane,
		Kind:   "control_plane_db",
		Status: StatusRunning,
	}
	id, err := s.Store.InsertBackup(ctx, &rec)
	if err != nil {
		return Record{}, err
	}
	rec.ID = id

	if err := s.dumpToFile(ctx, conn.Conn(), tables, path); err != nil {
		_, _ = s.Store.CompleteBackup(ctx, id, StatusFailed, 0, "", "", path, "dump başarısız: "+truncate(err.Error(), 512))
		return rec, err
	}

	st, serr := os.Stat(path)
	size := int64(0)
	if serr == nil {
		size = st.Size()
	}

	// A dump is not complete until integrity verification is recorded (kriter 2).
	vr, err := s.verifyFile(ctx, path)
	if err != nil {
		_, _ = s.Store.CompleteBackup(ctx, id, StatusFailed, size, "", "", path, "doğrulama başarısız: "+truncate(err.Error(), 512))
		return rec, err
	}
	rec, err = s.Store.CompleteBackup(ctx, id, StatusSuccess, size, vr.sha, "", path, vr.summary)
	if err != nil {
		return rec, err
	}
	return rec, nil
}

// dumpToFile streams the logical dump. Data leaves the database inside ONE
// REPEATABLE READ transaction (a single consistent snapshot — database-native
// consistency without copying live files). Row counts are patched into the
// fixed-width fields after each COPY; the footer sha256 is computed over the
// final bytes (pre-footer) once the patched file is re-read.
func (s *Service) dumpToFile(ctx context.Context, conn *pgx.Conn, tables []tableRef, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("backup: dosya: %w", err)
	}
	defer f.Close()

	bw := bufio.NewWriterSize(f, 128<<10)
	cw := &cappedWriter{w: bw, limit: MaxBackupBytes}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("backup: tutarlılık işlemi: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ`); err != nil {
		return fmt.Errorf("backup: izolasyon: %w", err)
	}

	fmt.Fprintf(cw, "%s\nformat=copy\ncreated_at=%s\ntables=%d\n\n",
		FormatMarker, time.Now().UTC().Format(time.RFC3339), len(tables))

	type rowsPatch struct {
		offset int64
		count  int64
	}
	var patches []rowsPatch
	totalRows := int64(0)

	for _, t := range tables {
		pad := strings.Repeat("0", rowsFieldWidth)
		if _, err := fmt.Fprintf(cw, "TABLE %s rows=%s\n", t.qualified(), pad); err != nil {
			return err
		}
		// The fixed-width padding starts rowsFieldWidth+1 bytes back
		// (digits + newline); it is patched in place once the COPY
		// stream has delivered the real row count.
		padOffset := cw.bytes - int64(rowsFieldWidth) - 1
		data := &countWriter{w: cw, limit: MaxBackupBytes}
		if _, err := tx.Conn().PgConn().CopyTo(ctx, data, "COPY "+t.qualified()+" TO STDOUT"); err != nil {
			return fmt.Errorf("backup: COPY %s: %w", t.qualified(), pgErr(err))
		}
		if _, err := fmt.Fprint(cw, "ENDTABLE\n"); err != nil {
			return err
		}
		patches = append(patches, rowsPatch{offset: padOffset, count: data.lines})
		totalRows += data.lines
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("backup: yazma: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("backup: fsync: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("backup: snapshot işlemi: %w", err)
	}

	// Patch the fixed-width row counters in place.
	for _, p := range patches {
		if _, err := f.WriteAt([]byte(fmt.Sprintf("%0*d", rowsFieldWidth, p.count)), p.offset); err != nil {
			return fmt.Errorf("backup: satır sayısı yazılamadı: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("backup: fsync: %w", err)
	}

	// Footer: sha256 over every byte written so far (pre-footer content).
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup: seek: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("backup: özet: %w", err)
	}
	if _, err := fmt.Fprintf(f, "FOOTER tables=%d rows=%d sha256=%s\n",
		len(tables), totalRows, hex.EncodeToString(h.Sum(nil))); err != nil {
		return fmt.Errorf("backup: footer: %w", err)
	}
	return f.Sync()
}

// verifyResult carries verification outcomes.
type verifyResult struct {
	size    int64
	sha     string
	summary string
	tables  int
	rows    int64
}

// verifyFile re-reads the artifact from disk and validates structure,
// integrity (footer sha256) and — when key material is known — that the
// encryption master key never leaked into the backup (kriter 3, fail-closed).
func (s *Service) verifyFile(ctx context.Context, path string) (verifyResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return verifyResult{}, fmt.Errorf("backup: açma: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return verifyResult{}, err
	}
	vr, err := verifyStream(bufio.NewReaderSize(f, 256<<10))
	if err != nil {
		return vr, err
	}
	// Kriter 3: the master key must never ride inside the artifact. The scan
	// runs on a second pass (structural verification already succeeded — a
	// leak still fails the whole verification).
	if s.KeyMaterial != "" {
		if _, serr := f.Seek(0, io.SeekStart); serr == nil {
			if containsPattern(bufio.NewReaderSize(f, 256<<10), []byte(s.KeyMaterial)) {
				return verifyResult{}, ErrKeyMaterialInBackup
			}
		}
	}
	vr.size = st.Size()
	return vr, nil
}

// verifyStream parses and validates the artifact format:
//
//	SCP-BACKUP-V1
//	format=copy
//	created_at=<RFC3339>
//	tables=<n>
//	<blank>
//	TABLE "public"."name" rows=<16-digit zero padded>
//	<copy text data, exactly rows lines>
//	ENDTABLE
//	...
//	FOOTER tables=<n> rows=<n> sha256=<hex over all preceding bytes>
func verifyStream(r io.Reader) (verifyResult, error) {
	br := bufio.NewReaderSize(r, 256<<10)
	hash := sha256.New()

	readLine := func() ([]byte, error) {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			hash.Write(line)
		}
		return line, err
	}
	// readLineNoHash reads without feeding the running hash (the footer
	// records the sha256 of all PRE-footer bytes).
	readLineNoHash := func() ([]byte, error) {
		return br.ReadBytes('\n')
	}

	first, err := readLine()
	if err != nil || strings.TrimRight(string(first), "\n") != FormatMarker {
		return verifyResult{}, ErrCorruptBackup
	}
	header := 0
	for {
		line, err := readLine()
		if err != nil {
			return verifyResult{}, ErrCorruptBackup
		}
		s := strings.TrimRight(string(line), "\n")
		if s == "" {
			break
		}
		if !strings.Contains(s, "=") || strings.HasPrefix(s, "TABLE ") || strings.HasPrefix(s, "FOOTER ") {
			return verifyResult{}, ErrCorruptBackup
		}
		header++
		if header > 16 {
			return verifyResult{}, ErrCorruptBackup
		}
	}

	tables, rows := 0, int64(0)
	for {
		// Peek without hashing: the FOOTER line is excluded from the running
		// sha256 (it records the hash of all pre-footer bytes).
		peek, _ := br.Peek(7)
		var line []byte
		var err error
		if len(peek) == 7 && string(peek) == "FOOTER " {
			line, err = readLineNoHash()
		} else {
			line, err = readLine()
		}
		if err != nil {
			return verifyResult{}, ErrCorruptBackup
		}
		s := strings.TrimRight(string(line), "\n")
		switch {
		case strings.HasPrefix(s, "TABLE "):
			name, nRows, ok := parseTableHeader(s)
			if !ok {
				return verifyResult{}, ErrCorruptBackup
			}
			for i := int64(0); i < nRows; i++ {
				dl, derr := readLine()
				if derr != nil {
					return verifyResult{}, fmt.Errorf("%w: satır eksik (%s)", ErrCorruptBackup, name)
				}
				if len(dl) > maxDataLine {
					return verifyResult{}, fmt.Errorf("%w: aşırı satır (%s)", ErrCorruptBackup, name)
				}
			}
			end, eerr := readLine()
			if eerr != nil || strings.TrimRight(string(end), "\n") != "ENDTABLE" {
				return verifyResult{}, fmt.Errorf("%w: ENDTABLE bekleniyordu (%s)", ErrCorruptBackup, name)
			}
			tables++
			rows += nRows
		case strings.HasPrefix(s, "FOOTER "):
			wantTables, wantRows, wantSha, ok := parseFooter(s)
			if !ok || wantTables != tables || wantRows != rows {
				return verifyResult{}, fmt.Errorf("%w: footer sayıları uyuşmuyor", ErrCorruptBackup)
			}
			if got := hex.EncodeToString(hash.Sum(nil)); got != wantSha {
				return verifyResult{}, fmt.Errorf("%w: sha256 uyuşmuyor", ErrCorruptBackup)
			}
			summary := fmt.Sprintf("tablo=%d satır=%d", tables, rows)
			return verifyResult{sha: wantSha, summary: summary, tables: tables, rows: rows}, nil
		default:
			return verifyResult{}, fmt.Errorf("%w: beklenmeyen satır: %q", ErrCorruptBackup, truncate(s, 32))
		}
	}
}

// maxDataLine bounds a single COPY data line in the artifact (paranoia; CP
// rows are small).
const maxDataLine = 1 << 20

func parseTableHeader(s string) (string, int64, bool) {
	rest := s[len("TABLE "):]
	i := strings.LastIndex(rest, " rows=")
	if i <= 0 {
		return "", 0, false
	}
	name := rest[:i]
	digits := rest[i+len(" rows="):]
	if len(digits) != rowsFieldWidth {
		return "", 0, false
	}
	var n int64
	if _, err := fmt.Sscanf(digits, "%d", &n); err != nil || n < 0 {
		return "", 0, false
	}
	if !strings.Contains(name, ".") {
		return "", 0, false
	}
	return name, n, true
}

func parseFooter(s string) (int, int64, string, bool) {
	var tables int
	var rows int64
	var sha string
	n, err := fmt.Sscanf(s, "FOOTER tables=%d rows=%d sha256=%64s", &tables, &rows, &sha)
	if err != nil || n != 3 {
		return 0, 0, "", false
	}
	if tables < 0 || rows < 0 {
		return 0, 0, "", false
	}
	return tables, rows, sha, true
}

// containsPattern reports whether the pattern bytes appear in the stream
// (sliding window with overlap so boundary splits are detected).
func containsPattern(r io.Reader, pattern []byte) bool {
	if len(pattern) == 0 {
		return false
	}
	buf := make([]byte, 256<<10)
	var tail []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append(tail, buf[:n]...)
			if strings.Contains(string(chunk), string(pattern)) {
				return true
			}
			if len(pattern) > 1 && len(chunk) > len(pattern)-1 {
				tail = append([]byte{}, chunk[len(chunk)-(len(pattern)-1):]...)
			} else {
				tail = nil
			}
		}
		if err != nil {
			return false
		}
	}
}

// pgErr unwraps pgconn.PgError into a readable message (no stack leakage —
// callers truncate).
func pgErr(err error) error {
	var pge *pgconn.PgError
	if e, ok := err.(*pgconn.PgError); ok {
		pge = e
		return fmt.Errorf("pg %s: %s", pge.Code, pge.Message)
	}
	return err
}

// truncate bounds a string for storage (detail columns have CHECK limits).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// randHex8 returns a random uint32 rendered as 8 lowercase hex chars.
func randHex8() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint32(b[:], uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}

// liveDBName extracts the database name from the connection URL.
func liveDBName(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil || strings.TrimPrefix(u.Path, "/") == "" {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// drillURL returns the connection URL for an isolated drill database.
func drillURL(databaseURL, drillDB string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("backup: URL çözümlenemedi")
	}
	u.Path = "/" + drillDB
	return u.String(), nil
}
