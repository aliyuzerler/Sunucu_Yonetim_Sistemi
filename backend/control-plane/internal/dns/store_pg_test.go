package dns

// PG entegrasyonu (CI'da gerçek PostgreSQL, yerelde TEST_DATABASE_URL
// olmadan atlanır — certmgmt/service_pg_test.go'daki CI-21 dersi).
// Kapsam: cloudflare türü sağlayıcının kalıcılık yuvarlama turu —
// kimlik bilgisi doğrulaması BuildCloudflare'da YAPILIR ama API çağrısı
// yapılmaz (sahte token güvenlidir); mühürlü blob'dan sağlayıcı yeniden
// inşa edilir (buildProvider → *CloudflareProvider).
import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func TestStoreCloudflareProviderRoundtrip(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	key, err := secrets.Generate()
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: NewStore(pool), Key: key}
	ctx := context.Background()

	row, err := svc.CreateProvider(ctx, "cf-uretim", "cloudflare", json.RawMessage(`{"api_token":"cf-TEST-token-0002"}`))
	if err != nil {
		t.Fatalf("cloudflare sağlayıcı kaydı: %v", err)
	}
	if row.Kind != "cloudflare" {
		t.Fatalf("tür kalıcı olmalı: %q", row.Kind)
	}
	if !row.Capabilities.SupportsDNS01 || row.Capabilities.TTLMin != 60 {
		t.Fatalf("capabilities kaydı: %+v", row.Capabilities)
	}
	if len(row.CredKeyNames) != 1 || row.CredKeyNames[0] != "api_token" {
		t.Fatalf("anahtar adları: %v", row.CredKeyNames)
	}
	if row.HasCredentials != true {
		t.Fatal("has_credentials true olmalı")
	}

	// Listeleme kimlik bilgisi İÇERMEZ (yalnız anahtar adları).
	rows, err := svc.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("liste: %+v", rows)
	}

	// buildProvider: mühürlü blob → *CloudflareProvider (yeniden inşa).
	p, err := svc.buildProvider(ctx, row.ID)
	if err != nil {
		t.Fatalf("yeniden inşa: %v", err)
	}
	if _, ok := p.(*CloudflareProvider); !ok {
		t.Fatalf("tür *CloudflareProvider olmalı: %T", p)
	}
	if p.Capabilities().TTLMax != 86400 {
		t.Fatalf("yeniden inşa edilen caps: %+v", p.Capabilities())
	}

	// Mock sağlayıcı ile yan yana yaşam: iki tür aynı tabloda.
	if _, err := svc.CreateProvider(ctx, "mock-test", "mock", json.RawMessage(`{"api_key":"k"}`)); err != nil {
		t.Fatalf("mock kaydı: %v", err)
	}
	rows, err = svc.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("iki sağlayıcı beklenir: %d", len(rows))
	}
}
