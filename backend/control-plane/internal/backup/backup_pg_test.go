package backup

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/db/migrations"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db/migrate"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

// testService builds a Service wired to the package's unique disposable
// database. Skips when TEST_DATABASE_URL is unset (CI provisions PostgreSQL).
func testService(t *testing.T, keyMaterial string) (*Service, context.CancelFunc) {
	t.Helper()
	pool := testdb.Pool(t)
	src, err := migrations.All()
	if err != nil {
		t.Fatalf("migration çiftleri: %v", err)
	}
	pairs := make([]migrate.Pair, 0, len(src))
	for _, p := range src {
		pairs = append(pairs, migrate.Pair{Version: p.Version, Name: p.Name, UpSQL: p.UpSQL, DownSQL: p.DownSQL})
	}
	svc := &Service{
		Store:       NewPGStore(pool),
		BackupDir:   t.TempDir(),
		DatabaseURL: testdb.CurrentURL(t),
		KeyMaterial: keyMaterial,
		Migrations:  pairs,
	}
	return svc, func() { _ = pool }
}

func TestControlPlaneBackupVerifyAndDrillPG(t *testing.T) {
	svc, done := testService(t, "CITEST-FAKE-KEY-0000")
	defer done()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Birkaç güvenli fixture satırı (platform_metadata serbest biçimlidir).
	testdb.MustExec(t, pool, `INSERT INTO platform_metadata (key, value) VALUES ('citest_row_1', '{"n":1}'::jsonb), ('citest_row_2', '{"n":2}'::jsonb)`)

	// 1) Dump + doğrulama kaydı (kriter 2).
	rec, err := svc.Dump(ctx, pool)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if rec.Status != StatusSuccess {
		t.Fatalf("durum: %+v", rec)
	}
	if rec.VerifyState != VerifyVerified || len(rec.SHA256) != 64 || rec.SizeBytes <= 0 {
		t.Fatalf("doğrulama kaydı: %+v", rec)
	}
	if !strings.HasSuffix(rec.StoragePath, ".scpbackup") {
		t.Fatalf("artifact yolu: %q", rec.StoragePath)
	}
	if _, err := os.Stat(rec.StoragePath); err != nil {
		t.Fatalf("artifact: %v", err)
	}

	// 2) Yeniden doğrulama (backup.cp.verify executor'ının yolu).
	rec2, err := svc.VerifyControlPlane(ctx, rec.ID)
	if err != nil {
		t.Fatalf("VerifyControlPlane: %v", err)
	}
	if rec2.VerifyState != VerifyVerified {
		t.Fatalf("yeniden doğrulama: %+v", rec2)
	}

	// 3) İZOLE restore tatbikatı (kriter 2) — şema (gömülü migration'lar)
	// + veri (COPY) hedefte birebir.
	drill, err := svc.RestoreDrill(ctx, pool, rec.ID, "citest")
	if err != nil {
		t.Fatalf("RestoreDrill: %v", err)
	}
	if drill.Status != StatusSuccess {
		t.Fatalf("tatbikat: %+v", drill)
	}
	if drill.TablesRestored == 0 || drill.RowsRestored == 0 {
		t.Fatalf("tatbikat sayıları: %+v", drill)
	}
	if !strings.HasPrefix(drill.Target, drillDBPrefix) {
		t.Fatalf("tatbikat hedefi izole ad taşımali: %+v", drill)
	}

	// 4) Bozulmuş artifact dürüstçe failed raporlanır.
	f, ferr := os.OpenFile(rec.StoragePath, os.O_RDWR, 0o600)
	if ferr != nil {
		t.Fatal(ferr)
	}
	st, _ := f.Stat()
	_, _ = f.WriteAt([]byte("Z"), st.Size()-80) // footer civarında tek bayt
	_ = f.Close()
	rec3, err := svc.VerifyControlPlane(ctx, rec.ID)
	if err == nil && rec3.VerifyState != VerifyFailed {
		t.Fatalf("bozuk artifact failed raporlanmalı: %+v", rec3)
	}
}

func TestDumpKeyMaterialLeakDetectedPG(t *testing.T) {
	// Kriter 3 fail-closed kanıtı: veriye sızmış anahtar malzemesi
	// doğrulamayı REDDETİRİR (sessiz kabul yok).
	leak := "CITEST-LEAKED-KEY-MATERIAL-9f2a"
	svc, done := testService(t, leak)
	defer done()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Anahtar malzemesini VERİ olarak ekle (sızma simülasyonu).
	testdb.MustExec(t, pool, `INSERT INTO platform_metadata (key, value) VALUES ('citest_leak', $1::jsonb)`, `"`+leak+`"`)

	_, err := svc.Dump(ctx, pool)
	if !errors.Is(err, ErrKeyMaterialInBackup) {
		t.Fatalf("sızma tespiti: %v", err)
	}
}
