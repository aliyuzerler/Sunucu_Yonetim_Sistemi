package dbcred

// Scrub birim testleri — PostgreSQL gerektirmez (sahte sealStore).
// Amaç (kriter 2): şifrenin KALICILAŞAN herhangi bir yüzeye düz metin
// girmemesi; her başarısızlık yolunda redaksiyonun kazanması.

import (
	"context"
	"strings"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
)

type fakeStore struct {
	upserted   int
	deleted    int
	upsertErr  error
	lastUser   string
	lastSealed []byte
}

func (f *fakeStore) Upsert(_ context.Context, _, _, username, _ string, sealed []byte, _ string) (Credential, error) {
	if f.upsertErr != nil {
		return Credential{}, f.upsertErr
	}
	f.upserted++
	f.lastUser = username
	f.lastSealed = sealed
	return Credential{ID: "11111111-2222-4333-8444-555566667777", Fingerprint: "abc123"}, nil
}

func (f *fakeStore) DeleteForUser(_ context.Context, _, _, _ string) (int, error) {
	f.deleted++
	return 1, nil
}

func (f *fakeStore) Reveal(_ context.Context, _ string) ([]byte, Credential, bool, bool, error) {
	return nil, Credential{}, false, false, ErrNotFound
}

func (f *fakeStore) List(_ context.Context, _ string) ([]Credential, error) {
	return nil, nil
}

func testKey(t *testing.T) secrets.Key {
	t.Helper()
	k, err := secrets.Generate()
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	return k
}

func nodeID() *string {
	s := "0aa1c3e1-1111-4222-8333-444455556666"
	return &s
}

func userCreateTask() *tasks.Task {
	return &tasks.Task{ID: "t1", Operation: OpDBUserCreate, NodeID: nodeID()}
}

// Normal yol: şifre sonuçtan AYRIŞIR, sealed blob store'a gider, sonuçta
// yalnız credential_id + fingerprint + one_time_reveal kalır.
func TestScrubUserCreateSealsAndStrips(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, Key: testKey(t), KeyConfigured: true}
	result := map[string]any{
		"kind": "postgres",
		"user": map[string]any{"name": "app_ro"},
		"credentials": map[string]any{
			"password":    "s3cr3t-password-XYZ",
			"fingerprint": "deadbeef",
		},
	}
	if err := svc.ScrubNodeResult(context.Background(), userCreateTask(), result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	cred := result["credentials"].(map[string]any)
	if _, leak := cred["password"]; leak {
		t.Fatal("şifre sonuçta kalmamalı")
	}
	if fs.upserted != 1 || fs.lastUser != "app_ro" {
		t.Fatalf("upsert bekleniyordu: %d %q", fs.upserted, fs.lastUser)
	}
	if len(fs.lastSealed) == 0 || strings.Contains(string(fs.lastSealed), "s3cr3t") {
		t.Fatal("sealed blob GCM ciphertext olmalı (düz metin değil)")
	}
	if cred["credential_id"] == nil || cred["one_time_reveal"] != true {
		t.Fatalf("reveal referansları eksik: %v", cred)
	}
}

// Idempotent replay: credentials bloğu yoksa scrub sessizce no-op.
func TestScrubUserCreateNoCredentialsNoop(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, Key: testKey(t), KeyConfigured: true}
	result := map[string]any{"kind": "postgres", "already_exists": true}
	if err := svc.ScrubNodeResult(context.Background(), userCreateTask(), result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if fs.upserted != 0 {
		t.Fatal("credentials yoksa upsert yapılmamalı")
	}
}

// Anahtar yoksa: şifre redakte edilir, store'a HİÇBİR ŞEY yazılmaz,
// dürüst redaksiyon gerekçesi sonuçta görünür.
func TestScrubUserCreateWithoutKeyRedacts(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, KeyConfigured: false}
	result := map[string]any{
		"kind":        "mysql",
		"user":        map[string]any{"name": "u1", "host": "localhost"},
		"credentials": map[string]any{"password": "topsecret"},
	}
	if err := svc.ScrubNodeResult(context.Background(), userCreateTask(), result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	cred := result["credentials"].(map[string]any)
	if _, leak := cred["password"]; leak {
		t.Fatal("şifre sonuçta kalmamalı")
	}
	if fs.upserted != 0 {
		t.Fatal("anahtarsız upsert yapılmamalı")
	}
	if cred["password_redacted"] != "true" || cred["redaction_reason"] == nil {
		t.Fatalf("dürüst redaksiyon gerekçesi eksik: %v", cred)
	}
}

// Provenance eksikse (node yok): fail-closed — şifre redakte edilir, store'a yazılmaz.
func TestScrubUserCreateWithoutNodeRedacts(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, Key: testKey(t), KeyConfigured: true}
	result := map[string]any{
		"kind":        "postgres",
		"user":        map[string]any{"name": "u1"},
		"credentials": map[string]any{"password": "p"},
	}
	tk := userCreateTask()
	tk.NodeID = nil
	if err := svc.ScrubNodeResult(context.Background(), tk, result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if fs.upserted != 0 {
		t.Fatal("provenance eksikken upsert yapılmamalı")
	}
	cred := result["credentials"].(map[string]any)
	if _, leak := cred["password"]; leak {
		t.Fatal("şifre sonuçta kalmamalı")
	}
}

// Seal hatası: redaksiyon kazanır (sonuç asla düz şifreyle kalıcılaşmaz).
func TestScrubUserCreateUpsertErrorRedacts(t *testing.T) {
	fs := &fakeStore{upsertErr: context.DeadlineExceeded}
	svc := &Service{Store: fs, Key: testKey(t), KeyConfigured: true}
	result := map[string]any{
		"kind":        "postgres",
		"user":        map[string]any{"name": "u1"},
		"credentials": map[string]any{"password": "p"},
	}
	if err := svc.ScrubNodeResult(context.Background(), userCreateTask(), result); err != nil {
		t.Fatalf("scrub hata döndürmemeli (redaksiyon kazanır): %v", err)
	}
	cred := result["credentials"].(map[string]any)
	if _, leak := cred["password"]; leak {
		t.Fatal("şifre sonuçta kalmamalı")
	}
	if cred["redaction_reason"] == nil {
		t.Fatal("redaksiyon gerekçesi olmalı")
	}
}

// db.user.delete: kullanıcının kimlik satırları düşürülür.
func TestScrubUserDeleteRemovesCredential(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, KeyConfigured: true}
	result := map[string]any{
		"kind": "mysql",
		"user": map[string]any{"name": "gone"},
	}
	tk := &tasks.Task{ID: "t2", Operation: OpDBUserDelete, NodeID: nodeID()}
	if err := svc.ScrubNodeResult(context.Background(), tk, result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if fs.deleted != 1 {
		t.Fatalf("DeleteForUser bekleniyordu: %d", fs.deleted)
	}
	if result["credentials_removed"] != true {
		t.Fatal("credentials_removed işaretlenmeli")
	}
}

// db.* dışı operasyonlar scrub'a takılmaz.
func TestScrubIgnoresOtherOperations(t *testing.T) {
	fs := &fakeStore{}
	svc := &Service{Store: fs, Key: testKey(t), KeyConfigured: true}
	result := map[string]any{"credentials": map[string]any{"password": "x"}}
	tk := &tasks.Task{ID: "t3", Operation: "web.site.create", NodeID: nodeID()}
	if err := svc.ScrubNodeResult(context.Background(), tk, result); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if fs.upserted != 0 {
		t.Fatal("db.* dışı operasyon seal tetiklememeli")
	}
	if result["credentials"].(map[string]any)["password"] != "x" {
		t.Fatal("db.* dışı sonuç değiştirilmemeli")
	}
}

// Fingerprint: 16 hex, tekrar üretilebilir.
func TestFingerprint(t *testing.T) {
	a := Fingerprint("password123")
	b := Fingerprint("password123")
	c := Fingerprint("password124")
	if a != b || a == c || len(a) != 16 {
		t.Fatalf("fingerprint tutarsız: %q %q %q", a, b, c)
	}
}
