package extensions

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

// fakeStore is a hermetic in-memory Store for service tests.
type fakeStore struct {
	exts   map[string]Extension
	raw    map[string]json.RawMessage
	grants map[string][]Grant
	next   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{exts: map[string]Extension{}, raw: map[string]json.RawMessage{}, grants: map[string][]Grant{}}
}

func (f *fakeStore) Upsert(_ context.Context, e Extension, manifest json.RawMessage) (Extension, error) {
	if e.ID == "" {
		f.next++
		e.ID = "00000000-0000-4000-8000-00000000000" + string(rune('0'+f.next))
	}
	e.Manifest = manifest
	f.exts[e.ID] = e
	f.raw[e.ID] = manifest
	return e, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (Extension, json.RawMessage, error) {
	e, ok := f.exts[id]
	if !ok {
		return Extension{}, nil, ErrNotFound
	}
	e.Granted = append([]Grant{}, f.grants[id]...)
	return e, f.raw[id], nil
}

func (f *fakeStore) GetByName(_ context.Context, name string) (Extension, json.RawMessage, error) {
	for _, e := range f.exts {
		if e.Name == name {
			return f.Get(context.Background(), e.ID)
		}
	}
	return Extension{}, nil, ErrNotFound
}

func (f *fakeStore) List(_ context.Context) ([]Extension, error) {
	var out []Extension
	for _, e := range f.exts {
		e.Granted = append([]Grant{}, f.grants[e.ID]...)
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeStore) UpsertGrant(_ context.Context, extID string, g Grant) (Grant, error) {
	for i, cur := range f.grants[extID] {
		if cur.Action == g.Action {
			f.grants[extID][i].RevokedAt = nil
			f.grants[extID][i].GrantedAt = g.GrantedAt
			return f.grants[extID][i], nil
		}
	}
	f.grants[extID] = append(f.grants[extID], g)
	return g, nil
}

func (f *fakeStore) RevokeGrant(_ context.Context, extID, action string) error {
	for i, cur := range f.grants[extID] {
		if cur.Action == action && cur.RevokedAt == nil {
			now := timeNow()
			f.grants[extID][i].RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeStore) RevokeAllGrants(_ context.Context, extID string) error {
	for i := range f.grants[extID] {
		if f.grants[extID][i].RevokedAt == nil {
			now := timeNow()
			f.grants[extID][i].RevokedAt = &now
		}
	}
	return nil
}

func timeNow() time.Time { return time.Now().UTC() }

func newService(t *testing.T, trustCount int) (*Service, *fakeStore, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	trust := []string{}
	if trustCount > 0 {
		trust = append(trust, base64.StdEncoding.EncodeToString(pub))
	}
	st := newFakeStore()
	svc, err := NewService(st, trust, "1.0.0", "0.16.0-rc1",
		func() map[string]bool { return map[string]bool{"backup.cp.run": true, "backup.repo.init": true} },
		func() map[string]bool {
			return map[string]bool{"backup:read": true, "backup:control": true, "nodes:read": true}
		})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, st, priv, pub
}

// signedManifest builds a valid manifest and signs its canonical bytes.
func signedManifest(t *testing.T, priv ed25519.PrivateKey, mutate func(m *Manifest)) (json.RawMessage, Manifest) {
	t.Helper()
	m := Manifest{
		ManifestVersion: ManifestVersion,
		Name:            "demo-ext",
		DisplayName:     "Demo Extension",
		Vendor:          "Example GmbH",
		Version:         "1.2.3",
		APIVersion:      ">=1.0.0 <2.0.0",
		MinCPVersion:    "0.15.0",
		ArtifactSHA256:  strings.Repeat("ab", 32),
		Permissions:     []PermRequest{{Action: "backup:read", Risk: "low", Reason: "yedek listesini okur"}},
		DelegatedOps:    []string{"backup.cp.run"},
		Capabilities:    []string{"linux.systemd.service.restart"},
	}
	if mutate != nil {
		mutate(&m)
	}
	// Build raw JSON, REMOVE the signature field (signatures cover the
	// manifest WITHOUT the signature field — same rule as canonicalManifest),
	// canonicalize, sign, re-attach the signature.
	noSig, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(noSig, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	delete(obj, "signature")
	rest, _ := json.Marshal(obj)
	canonical, err := signpkg.CanonicalJSON(rest)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	sig := ed25519.Sign(priv, canonical)
	obj["signature"] = json.RawMessage(`"` + base64.StdEncoding.EncodeToString(sig) + `"`)
	raw, _ := json.Marshal(obj)
	return raw, m
}

func TestRegisterHappyPathPendingNoGrants(t *testing.T) {
	svc, st, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, nil)
	ext, err := svc.Register(context.Background(), "admin", raw)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if ext.State != "pending" {
		t.Fatalf("durum pending olmalı: %s", ext.State)
	}
	if len(ext.Granted) != 0 {
		t.Fatalf("varsayılan izin VERİLEMEZ: %v", ext.Granted)
	}
	if !ext.Compatible {
		t.Fatalf("API uyumlu olmalı")
	}
	if _, ok := st.exts[ext.ID]; !ok {
		t.Fatalf("kayıt saklanmadı")
	}
}

func TestRegisterNoTrustKeyFailClosed(t *testing.T) {
	svc, _, priv, _ := newService(t, 0)
	raw, _ := signedManifest(t, priv, nil)
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrNoTrustKey) {
		t.Fatalf("ErrNoTrustKey beklenir: %v", err)
	}
}

func TestRegisterBadSignatureRejected(t *testing.T) {
	svc, _, priv, other := newService(t, 1)
	_ = priv
	// Sign with a DIFFERENT key than the trusted one.
	_, _, otherPriv, _ := newService(t, 0)
	_ = other
	raw, _ := signedManifest(t, otherPriv, nil)
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("ErrBadSignature beklenir: %v", err)
	}
	// Tampered manifest: mutate the SIGNED bytes after the fact (version
	// value swapped in the raw JSON — canonical bytes no longer match sig).
	raw, _ = signedManifest(t, priv, nil)
	tampered := []byte(strings.Replace(string(raw), "1.2.3", "9.9.9", 1))
	if _, err := svc.Register(context.Background(), "admin", tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("değiştirilmiş manifest reddedilmedi: %v", err)
	}
}

func TestRegisterAPIIncompatible(t *testing.T) {
	svc, _, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, func(m *Manifest) { m.APIVersion = ">=2.0.0" })
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrAPIIncompatible) {
		t.Fatalf("ErrAPIIncompatible beklenir: %v", err)
	}
	raw, _ = signedManifest(t, priv, func(m *Manifest) { m.MinCPVersion = "99.0.0" })
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrAPIIncompatible) {
		t.Fatalf("min_cp_version uyumsuzluğu yakalanmadı: %v", err)
	}
}

func TestRegisterUnknownPermissionAndOp(t *testing.T) {
	svc, _, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, func(m *Manifest) {
		m.Permissions = append(m.Permissions, PermRequest{Action: "root:everything", Risk: "low", Reason: "her şeyi yapar"})
	})
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("bilinmeyen izin reddedilmedi: %v", err)
	}
	raw, _ = signedManifest(t, priv, func(m *Manifest) {
		m.DelegatedOps = []string{"shell.exec.arbitrary"}
	})
	if _, err := svc.Register(context.Background(), "admin", raw); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("bilinmeyen op reddedilmedi: %v", err)
	}
}

func TestApproveOnlyRequestedPermissions(t *testing.T) {
	svc, _, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, nil)
	ext, err := svc.Register(context.Background(), "admin", raw)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// İstenmemiş izin onaylanamaz.
	if _, err := svc.ApprovePermission(context.Background(), ext.ID, "nodes:read"); !errors.Is(err, ErrPermNotRequested) {
		t.Fatalf("ErrPermNotRequested beklenir: %v", err)
	}
	ext, err = svc.ApprovePermission(context.Background(), ext.ID, "backup:read")
	if err != nil {
		t.Fatalf("ApprovePermission: %v", err)
	}
	if len(ext.Granted) != 1 || ext.Granted[0].Action != "backup:read" || ext.Granted[0].RevokedAt != nil {
		t.Fatalf("grant beklenir: %+v", ext.Granted)
	}
	ok, err := svc.HasPermission(context.Background(), ext.ID, "backup:read")
	if err != nil || !ok {
		t.Fatalf("HasPermission true beklenir: %v %v", ok, err)
	}
}

func TestRevokePermissionAndExtension(t *testing.T) {
	svc, _, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, nil)
	ext, _ := svc.Register(context.Background(), "admin", raw)
	ext, err := svc.ApprovePermission(context.Background(), ext.ID, "backup:read")
	if err != nil {
		t.Fatalf("onay: %v", err)
	}
	ext, err = svc.RevokePermission(context.Background(), ext.ID, "backup:read")
	if err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}
	if ok, _ := svc.HasPermission(context.Background(), ext.ID, "backup:read"); ok {
		t.Fatalf("geri alınan izin hâlâ aktif")
	}
	if _, err := svc.Revoke(context.Background(), ext.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err := svc.Get(context.Background(), ext.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "revoked" {
		t.Fatalf("durum revoked olmalı: %s", got.State)
	}
	if _, err := svc.ApprovePermission(context.Background(), ext.ID, "backup:read"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("revoked uzantıya onay verilememeli: %v", err)
	}
}

func TestReregisterResetsGrants(t *testing.T) {
	svc, _, priv, _ := newService(t, 1)
	raw, _ := signedManifest(t, priv, nil)
	ext, _ := svc.Register(context.Background(), "admin", raw)
	if _, err := svc.ApprovePermission(context.Background(), ext.ID, "backup:read"); err != nil {
		t.Fatalf("onay: %v", err)
	}
	// Yeni sürüm kaydı → grants sıfırlanmalı, state pending'e dönmeli.
	raw2, _ := signedManifest(t, priv, func(m *Manifest) { m.Version = "1.3.0" })
	ext2, err := svc.Register(context.Background(), "admin", raw2)
	if err != nil {
		t.Fatalf("yeniden kayıt: %v", err)
	}
	if ext2.ID != ext.ID {
		t.Fatalf("aynı ad aynı kayıt olmalı")
	}
	if ext2.State != "pending" {
		t.Fatalf("yeni sürüm pending olmalı: %s", ext2.State)
	}
	if len(ext2.Granted) != 0 || ext2.Granted == nil {
		// granted listesi boş olmalı (hepsi revoked veya kaldırılmış)
	}
	for _, g := range ext2.Granted {
		if g.RevokedAt == nil {
			t.Fatalf("eski grant aktif kaldı: %+v", g)
		}
	}
	ok, _ := svc.HasPermission(context.Background(), ext.ID, "backup:read")
	if ok {
		t.Fatalf("yeni sürümde eski izin etkili olmamalı")
	}
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-1", "1.0.0-alpha", -1}, // numeric < alphanumeric
		{"2.0.0", "1.9.9", 1},
		{"0.16.0-rc1", "0.15.0", 1},
	}
	for _, c := range cases {
		a, err1 := parseSemver(c.a)
		b, err2 := parseSemver(c.b)
		if err1 != nil || err2 != nil {
			t.Fatalf("parse: %v %v", err1, err2)
		}
		if got := cmpSemver(a, b); got != c.want {
			t.Fatalf("cmp(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAPIRangeMatching(t *testing.T) {
	r, err := ParseAPIRange(">=1.0.0 <2.0.0")
	if err != nil {
		t.Fatalf("ParseAPIRange: %v", err)
	}
	v, _ := parseSemver("1.5.0")
	if !r.Matches(v) {
		t.Fatalf("1.5.0 aralıkta olmalı")
	}
	v2, _ := parseSemver("2.0.0")
	if r.Matches(v2) {
		t.Fatalf("2.0.0 aralık dışı olmalı")
	}
	caret, _ := ParseAPIRange("^1.2.0")
	v3, _ := parseSemver("1.9.9")
	if !caret.Matches(v3) {
		t.Fatalf("^1.2.0 1.9.9'u kapsamalı")
	}
	v4, _ := parseSemver("2.0.0")
	if caret.Matches(v4) {
		t.Fatalf("^1.2.0 2.0.0'ı kapsamamalı")
	}
	if _, err := ParseAPIRange(""); err == nil {
		t.Fatalf("boş aralık reddedilmeli")
	}
	if _, err := ParseAPIRange(">=abc"); err == nil {
		t.Fatalf("bozuk kısıt reddedilmeli")
	}
}
