package dbcred

// PostgreSQL entegrasyon testleri — TEST_DATABASE_URL gerekir (CI'da
// PostgreSQL 18 service container; yerelde disposable compose).
// Upsert + tek-seferlik reveal + kullanıcı silme temizliği yaşam döngüsü.

import (
	"context"
	"strings"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

const pgTestNode = "0aa1c3e1-1111-4222-8333-444455556666"

func TestCredentialLifecyclePG(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	store := NewStore(pool)
	key, err := secrets.Generate()
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	ctx := context.Background()

	// Upsert + upsert yeniden (reveal penceresi resetlenir).
	c1, err := store.Upsert(ctx, pgTestNode, "postgres", "app_ro", "", []byte("sealed-blob-1"), "fp1")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if c1.ID == "" || c1.RevealedAt != nil {
		t.Fatalf("ilk satır hatalı: %+v", c1)
	}
	c2, err := store.Upsert(ctx, pgTestNode, "postgres", "app_ro", "", []byte("sealed-blob-2"), "fp2")
	if err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	if c1.ID != c2.ID {
		t.Fatalf("aynı (node,kind,user) aynı satır olmalı: %s vs %s", c1.ID, c2.ID)
	}

	// Tek-seferlik reveal: ilki blob döner, ikincisi typed red.
	sealed, cred, found, already, err := store.Reveal(ctx, c1.ID)
	if err != nil || !found || already {
		t.Fatalf("ilk reveal: err=%v found=%v already=%v", err, found, already)
	}
	if string(sealed) != "sealed-blob-2" || cred.Fingerprint != "fp2" {
		t.Fatalf("reveal materyali hatalı: %q %q", sealed, cred.Fingerprint)
	}
	if cred.RevealedAt == nil {
		t.Fatal("revealed_at set edilmeli")
	}
	_, _, _, already, err = store.Reveal(ctx, c1.ID)
	if err != nil || !already {
		t.Fatalf("ikinci reveal already olmalı: err=%v already=%v", err, already)
	}

	// Liste: blob sızdırmaz (Credential yapısı zaten blob taşımaz).
	rows, err := store.List(ctx, pgTestNode)
	if err != nil || len(rows) != 1 {
		t.Fatalf("liste: %v %d", err, len(rows))
	}

	// Kullanıcı silme: satır düşer; bilinmeyen id reveal → ErrNotFound.
	if n, err := store.DeleteForUser(ctx, pgTestNode, "postgres", "app_ro"); err != nil || n != 1 {
		t.Fatalf("delete: %v %d", err, n)
	}
	if _, _, found, _, err := store.Reveal(ctx, c1.ID); err != nil || found {
		t.Fatalf("silinen kimlik bulunmamalı: err=%v found=%v", err, found)
	}
	svc := &Service{Store: store, Key: key, KeyConfigured: true}
	if _, _, err := svc.Reveal(ctx, "11111111-2222-4333-8444-555566667777"); err != ErrNotFound {
		t.Fatalf("bilinmeyen id typed red olmalı: %v", err)
	}
}

// Anahtarsız servis reveal'i tipli reddeder (kriter 2 — tahmin yok).
func TestRevealWithoutKeyTyped(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	svc := &Service{Store: NewStore(pool), KeyConfigured: false}
	_, _, err := svc.Reveal(context.Background(), pgTestNode)
	if err != ErrKeyNotConfigured {
		t.Fatalf("ErrKeyNotConfigured bekleniyordu: %v", err)
	}
	// Hata metni şifre/bar relevant içermemeli.
	if strings.Contains(err.Error(), "password") {
		t.Fatal("hata metni sızdırıcı olmamalı")
	}
}
