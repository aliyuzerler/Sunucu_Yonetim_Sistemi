package dns

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

func testService(t *testing.T) *Service {
	t.Helper()
	k, err := secrets.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Key: k}
}

// MockPGStore, Service'in store'a dokunan yollarını DB'siz denetlemek için
// davranışsal test çiftidir (yalnız executor olmayan yollar). Create/
// Delete/List DB yolları pg entegrasyon testindedir.
func TestServiceCreateProviderValidates(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	// Faz 16: Cloudflare artık desteklenen tür — ancak kimlik bilgisi
	// doğrulaması yine de eksiksiz uygulanır (boş token; API çağrısı yok).
	if _, err := svc.CreateProvider(ctx, "cf", "cloudflare", json.RawMessage(`{"api_token":""}`)); err == nil {
		t.Fatal("boş api_token reddedilmeli")
	}
	if _, err := svc.CreateProvider(ctx, "cf", "cloudflare", json.RawMessage(`{"api_key":"x"}`)); err == nil {
		t.Fatal("cloudflare sözleşmesi api_token'dır — api_key reddedilmeli")
	}
	// Bilinmeyen tür hâlâ dürüstçe reddedilir (tahmin yok).
	if _, err := svc.CreateProvider(ctx, "r", "route53", json.RawMessage(`{}`)); err == nil {
		t.Fatal("route53 desteklenmiyor — reddedilmeli")
	}
	// Mock + boş kimlik bilgisi.
	if _, err := svc.CreateProvider(ctx, "m", "mock", json.RawMessage(`{"api_key":""}`)); err == nil {
		t.Fatal("boş api_key reddedilmeli")
	}
	// Anahtarsız servis.
	none := &Service{}
	if _, err := none.CreateProvider(ctx, "m", "mock", json.RawMessage(`{"api_key":"k"}`)); !IsKeyMissing(err) {
		t.Fatal("anahtarsız servis ErrKeyMissing döndürmeli")
	}
}

// IsKeyMissing classifies the unconfigured-key error.
func IsKeyMissing(err error) bool { return err != nil && err.Error() == ErrKeyMissing.Error() }

func TestResolveZone(t *testing.T) {
	zones := []string{"example.com", "sub.example.com", "other.org"}
	if z, n := resolveZone(zones, "www.example.com"); z != "example.com" || n != "_acme-challenge.www" {
		t.Fatalf("uzun eşleşme: %s/%s", z, n)
	}
	if z, n := resolveZone(zones, "example.com"); z != "example.com" || n != "_acme-challenge" {
		t.Fatalf("apex: %s/%s", z, n)
	}
	if z, n := resolveZone(zones, "deep.a.sub.example.com"); z != "sub.example.com" || n != "_acme-challenge.deep.a" {
		t.Fatalf("alt bölge önceliği: %s/%s", z, n)
	}
	if z, _ := resolveZone(zones, "nowhere.net"); z != "" {
		t.Fatalf("eşleşmeyen domain boş bölge döndürmeli: %s", z)
	}
}

func TestSetTXTRecordOnMock(t *testing.T) {
	// DB'siz doğrulama: Service köprüsünün sağlayıcı tarafı.
	svc := testService(t)
	p := NewMock()
	_ = p.SeedZone("example.com")
	_ = p.SeedZone("app.example.com")

	ctx := context.Background()
	if err := svc.setTXTOn(p, ctx, "example.com", "digest-1"); err != nil {
		t.Fatalf("apex TXT: %v", err)
	}
	if rec, ok := p.GetRecord("example.com", "_acme-challenge", "TXT"); !ok || rec.Value != "digest-1" {
		t.Fatalf("apex TXT kaydı: %+v ok=%v", rec, ok)
	}
	// İkinci çağrı upsert (update) — çakışma hatası YOK.
	if err := svc.setTXTOn(p, ctx, "example.com", "digest-2"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if rec, _ := p.GetRecord("example.com", "_acme-challenge", "TXT"); rec.Value != "digest-2" {
		t.Fatalf("upsert değeri: %+v", rec)
	}
	// Alt domain.
	if err := svc.setTXTOn(p, ctx, "www.app.example.com", "digest-3"); err != nil {
		t.Fatalf("sub TXT: %v", err)
	}
	if rec, ok := p.GetRecord("app.example.com", "_acme-challenge.www", "TXT"); !ok || rec.Value != "digest-3" {
		t.Fatalf("sub TXT kaydı: %+v ok=%v", rec, ok)
	}
	// Eşleşmeyen bölge.
	if err := svc.setTXTOn(p, ctx, "nowhere.net", "d"); err == nil {
		t.Fatal("bölgesiz domain reddedilmeli")
	}
}
