package certmgmt

// PostgreSQL entegrasyon testleri — TEST_DATABASE_URL gerekir (CI'da
// PostgreSQL 18 service container; yerelde disposable compose).
import (
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func TestStoreLifecycle(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	store := NewStore(pool)
	ctx := t.Context()

	rec, err := store.Create(ctx, Record{
		Name:       "example.internal",
		SANs:       []string{"example.internal", "www.example.internal"},
		ProviderID: "prov-1",
		NodeID:     "node-1",
		CSRPEM:     "-----BEGIN CERTIFICATE REQUEST-----\nTEST\n-----END CERTIFICATE REQUEST-----",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.State != StatePending {
		t.Fatalf("ilk durum pending olmalı: %s", rec.State)
	}

	// pending → issuing.
	if err := store.MarkIssuing(ctx, rec.ID); err != nil {
		t.Fatalf("issuing: %v", err)
	}
	// issuing kaydında issued materyali YOK.
	if _, _, err := store.GetCertMaterial(ctx, rec.ID); err == nil {
		t.Fatal("issuing aşamasında materyal olmamalı")
	}

	notAfter := time.Now().Add(90 * 24 * time.Hour)
	if err := store.MarkIssued(ctx, rec.ID, []byte("sealed-cert"), []byte("sealed-chain"), notAfter, "fp-abc"); err != nil {
		t.Fatalf("issued: %v", err)
	}
	certEnc, chainEnc, err := store.GetCertMaterial(ctx, rec.ID)
	if err != nil || string(certEnc) != "sealed-cert" || string(chainEnc) != "sealed-chain" {
		t.Fatalf("materyal: %v %s %s", err, certEnc, chainEnc)
	}

	// issued → renewing → renewal_failed: CANLI SERTİFİKA ETKİLENMEZ.
	if err := store.MarkRenewing(ctx, rec.ID); err != nil {
		t.Fatalf("renewing: %v", err)
	}
	if err := store.MarkFailed(ctx, rec.ID, StateRenewalFail, "rate_limit", "A: 5 per week", 3600); err != nil {
		t.Fatalf("renewal_failed: %v", err)
	}
	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateRenewalFail || got.LastErrorClass != "rate_limit" || got.RetryAfterSecs != 3600 {
		t.Fatalf("başarısızlık kaydı: %+v", got)
	}
	// Materyal hâlâ yerinde (canlı sertifika korunur — kriter 4).
	if certEnc, _, err = store.GetCertMaterial(ctx, rec.ID); err != nil || string(certEnc) != "sealed-cert" {
		t.Fatal("başarısız yenileme materyali bozmamalı")
	}

	// Yeniden issuing → issued (başarılı yenileme).
	if err := store.MarkRenewing(ctx, rec.ID); err != nil {
		t.Fatalf("renewing-2: %v", err)
	}
	if err := store.MarkIssued(ctx, rec.ID, []byte("sealed-cert-2"), []byte("sealed-chain-2"), notAfter, "fp-def"); err != nil {
		t.Fatalf("issued-2: %v", err)
	}

	// Revoke.
	if err := store.MarkRevoked(ctx, rec.ID); err != nil {
		t.Fatalf("revoked: %v", err)
	}
	got, _ = store.Get(ctx, rec.ID)
	if got.State != StateRevoked || got.RevokedAt.IsZero() {
		t.Fatalf("revoked: %+v", got)
	}

	// Geçersiz geçişler: pending olmayan kayda MarkIssuing red (typed state machine).
	if err := store.MarkIssuing(ctx, rec.ID); err == nil {
		t.Fatal("revoked kaydı issuing'e geçmemeli")
	}
	// MarkFailed yalnız pending/renewal_failed'e yazar.
	if err := store.MarkFailed(ctx, rec.ID, StateRenewalFail, "challenge", "x", 0); err == nil {
		t.Fatal("revoked kayıt renewal_failed'e düşmemeli")
	}
}

func TestStoreExpiryWatcher(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	store := NewStore(pool)
	ctx := t.Context()

	rec, err := store.Create(ctx, Record{Name: "short.internal", ProviderID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkIssuing(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.MarkIssued(ctx, rec.ID, []byte("c"), []byte(""), past, "fp"); err != nil {
		t.Fatal(err)
	}

	// Tembel efektif durum: okuma anında expired görünür.
	got, _ := store.Get(ctx, rec.ID)
	if got.State != StateIssued {
		t.Fatalf("kalıcı durum issued kalmalı (watcher yazar): %s", got.State)
	}
	if EffectiveStateOf(got, time.Now()) != StateExpired {
		t.Fatal("efektif durum expired olmalı")
	}

	// Watcher geçişi kalıcılaştırır.
	n, err := store.MarkExpired(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("watcher geçişi: n=%d err=%v", n, err)
	}
	got, _ = store.Get(ctx, rec.ID)
	if got.State != StateExpired {
		t.Fatalf("watcher sonrası state: %s", got.State)
	}
	// İkinci koşu no-op (idempotent).
	if n, _ := store.MarkExpired(ctx, time.Now()); n != 0 {
		t.Fatalf("idempotent olmalı: %d", n)
	}
}
