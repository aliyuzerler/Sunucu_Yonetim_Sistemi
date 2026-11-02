package cloud

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

// fakeStore records rows in memory. EncryptedCredentials is exposed through
// the optional interface the service decrypts through.
type fakeStore struct {
	rows map[string]ProviderRow
	blob map[string][]byte
	seq  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]ProviderRow{}, blob: map[string][]byte{}}
}

func (f *fakeStore) Create(ctx context.Context, name string, kind Kind, endpoint, region string, encrypted []byte, credKeys []string) (ProviderRow, error) {
	f.seq++
	id := "00000000-0000-4000-8000-00000000000" + string(rune('0'+f.seq))
	row := ProviderRow{ID: id, Name: name, Kind: kind, Endpoint: endpoint, Region: region, CredKeyNames: credKeys}
	f.rows[id] = row
	f.blob[id] = encrypted
	return row, nil
}
func (f *fakeStore) List(ctx context.Context) ([]ProviderRow, error) {
	out := []ProviderRow{}
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeStore) Get(ctx context.Context, id string) (ProviderRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return ProviderRow{}, ErrProviderNotFound
	}
	return r, nil
}
func (f *fakeStore) Delete(ctx context.Context, id string) error {
	delete(f.rows, id)
	return nil
}
func (f *fakeStore) EncryptedCredentials(ctx context.Context, id string) ([]byte, error) {
	b, ok := f.blob[id]
	if !ok {
		return nil, ErrProviderNotFound
	}
	return b, nil
}

var _ Store = (*fakeStore)(nil)
var _ interface {
	EncryptedCredentials(ctx context.Context, id string) ([]byte, error)
} = (*fakeStore)(nil)

func testKey(t *testing.T) secrets.Key {
	t.Helper()
	k, err := secrets.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestServiceCreateSealsCredentials(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, testKey(t))
	row, err := svc.Create(context.Background(), CreateInput{
		Name: "citest", Kind: KindS3, Endpoint: "http://127.0.0.1:9000", Region: "test-1",
		Credentials: map[string]string{"access_key_id": "AKIAfake000000000000", "secret_access_key": strings.Repeat("s", 40)},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Blob blob şifrelidir: düz metin anahtar içermemeli.
	blob := string(store.blob[row.ID])
	if strings.Contains(blob, strings.Repeat("s", 40)) {
		t.Fatal("düz metin secret blob'da ASLA bulunmamalı")
	}
	// Okuma yolları değer döndürmez.
	got, _ := svc.Get(context.Background(), row.ID)
	if len(got.CredKeyNames) != 2 {
		t.Fatalf("yalnız anahtar adları: %+v", got.CredKeyNames)
	}
	if strings.Contains(mustJSON(t, got), strings.Repeat("s", 40)) {
		t.Fatal("okuma yolu secret döndürmemeli")
	}
}

func TestServiceCreateValidations(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, testKey(t))
	ctx := context.Background()
	// Bilinmeyen kind.
	if _, err := svc.Create(ctx, CreateInput{Name: "abc", Kind: "azure", Endpoint: "https://x"}); !errorsIs(err, ErrUnknownKind) {
		t.Fatalf("bilinmeyen kind: %v", err)
	}
	// https dışı uç.
	if _, err := svc.Create(ctx, CreateInput{Name: "abc", Kind: KindS3, Endpoint: "http://example.com"}); !errorsIs(err, ErrInvalidEndpoint) {
		t.Fatalf("http red: %v", err)
	}
	// Eksik kimlik.
	if _, err := svc.Create(ctx, CreateInput{Name: "abc", Kind: KindS3, Endpoint: "https://s3.example.com",
		Credentials: map[string]string{"access_key_id": "AKIAfake000000000000"}}); !errorsIs(err, ErrCredIncomplete) {
		t.Fatalf("eksik secret: %v", err)
	}
	// Anahtar yapılandırılmamış → dürüst red.
	svcNoKey := NewService(store, nil)
	if _, err := svcNoKey.Create(ctx, CreateInput{Name: "abc", Kind: KindS3, Endpoint: "https://s3.example.com",
		Credentials: map[string]string{"access_key_id": "a", "secret_access_key": "b"}}); !errorsIs(err, ErrKeyNotConfigured) {
		t.Fatalf("anahtarsız kurulum red: %v", err)
	}
}

func TestServiceDeleteResourceConfirmGate(t *testing.T) {
	f := newFakeS3(t, "doomed")
	store := newFakeStore()
	svc := NewService(store, testKey(t))
	// Sahte S3 uç noktasını adaptöre taşı.
	svc.Providers[KindS3] = &S3Provider{Client: f.srv.Client()}
	row, err := svc.Create(context.Background(), CreateInput{
		Name: "citest", Kind: KindS3, Endpoint: f.srv.URL,
		Credentials: map[string]string{"access_key_id": "AKIAfake000000000000", "secret_access_key": strings.Repeat("s", 40)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Onay yok → tipli red, provider'a hiç çağrı gitmez.
	before := f.deletes.Load()
	if err := svc.DeleteResource(ctx, row.ID, "doomed", false); !errorsIs(err, ErrConfirmationRequired) {
		t.Fatalf("onaysız silme reddedilmeli: %v", err)
	}
	if f.deletes.Load() != before {
		t.Fatal("onaysız istek provider'a gitmemeli")
	}
	// Onay ile silinir.
	if err := svc.DeleteResource(ctx, row.ID, "doomed", true); err != nil {
		t.Fatalf("onaylı silme: %v", err)
	}
	if f.deletes.Load() != before+1 {
		t.Fatal("onaylı silme provider DELETE yapmalı")
	}
}

func TestServiceVerifyReportsMatrix(t *testing.T) {
	f := newFakeS3(t, "b1")
	store := newFakeStore()
	svc := NewService(store, testKey(t))
	svc.Providers[KindS3] = &S3Provider{Client: f.srv.Client()}
	row, err := svc.Create(context.Background(), CreateInput{
		Name: "citest", Kind: KindS3, Endpoint: f.srv.URL,
		Credentials: map[string]string{"access_key_id": "AKIAfake000000000000", "secret_access_key": strings.Repeat("s", 40)},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.VerifyCredentials(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("VerifyCredentials: %v", err)
	}
	if len(report.Matrix.Entries) != 3 || len(report.Probes) != 1 || !report.Probes[0].OK {
		t.Fatalf("rapor: %+v", report)
	}
	// Olmayan sağlayıcı tipli not found.
	if _, err := svc.VerifyCredentials(context.Background(), "yok-0000-0000"); !errorsIs(err, ErrProviderNotFound) {
		t.Fatalf("olmayan sağlayıcı: %v", err)
	}
}

// mustJSON marshals or fails the test (helper for leak assertions).
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
