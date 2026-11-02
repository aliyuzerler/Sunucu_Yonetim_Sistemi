package updates

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

type fakeStore struct {
	arts map[string]Artifact
	raw  map[string]json.RawMessage
	next int
}

func newFakeStore() *fakeStore {
	return &fakeStore{arts: map[string]Artifact{}, raw: map[string]json.RawMessage{}}
}

func (f *fakeStore) Insert(_ context.Context, a Artifact, manifest json.RawMessage) (Artifact, error) {
	f.next++
	a.ID = "00000000-0000-4000-8000-00000000000" + string(rune('0'+f.next))
	a.Manifest = manifest
	f.arts[a.ID] = a
	f.raw[a.ID] = manifest
	return a, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (Artifact, error) {
	a, ok := f.arts[id]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) GetByVersion(_ context.Context, target, version string) (Artifact, error) {
	for _, a := range f.arts {
		if a.Target == target && a.Version == version {
			return a, nil
		}
	}
	return Artifact{}, ErrNotFound
}

func (f *fakeStore) List(_ context.Context, target string) ([]Artifact, error) {
	var out []Artifact
	for _, a := range f.arts {
		if target == "" || a.Target == target {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) SetState(_ context.Context, id, state string) error {
	a, ok := f.arts[id]
	if !ok {
		return ErrNotFound
	}
	a.State = state
	f.arts[id] = a
	return nil
}

func newService(t *testing.T) (*Service, *fakeStore, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	st := newFakeStore()
	svc, err := NewService(st, []string{base64.StdEncoding.EncodeToString(pub)})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, st, priv
}

// signedUpdateManifest builds a valid signed update manifest.
func signedUpdateManifest(t *testing.T, priv ed25519.PrivateKey, mutate func(m *Manifest)) json.RawMessage {
	t.Helper()
	m := Manifest{
		ManifestVersion: ManifestVersion,
		Target:          TargetAgentLinux,
		Version:         "0.16.0-rc1",
		FileName:        "server-control-agent_0.16.0-rc1_amd64.deb",
		SizeBytes:       12_345_678,
		SHA256:          strings.Repeat("cd", 32),
		MinPrev:         "0.15.0",
		APIVersion:      "1.0.0",
		Notes:           "RC1 sürümü",
	}
	if mutate != nil {
		mutate(&m)
	}
	noSig, _ := json.Marshal(m)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(noSig, &obj)
	delete(obj, "signature")
	canonical, err := m.Canonical(mustRaw(t, obj))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig := ed25519.Sign(priv, canonical)
	obj["signature"] = json.RawMessage(`"` + base64.StdEncoding.EncodeToString(sig) + `"`)
	raw, _ := json.Marshal(obj)
	return raw
}

func mustRaw(t *testing.T, obj map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestPublishHappyPath(t *testing.T) {
	svc, st, priv := newService(t)
	raw := signedUpdateManifest(t, priv, nil)
	a, err := svc.Publish(context.Background(), "admin", raw)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if a.State != StatePublished || a.Target != TargetAgentLinux {
		t.Fatalf("kayıt hatalı: %+v", a)
	}
	if _, ok := st.arts[a.ID]; !ok {
		t.Fatalf("kayıt saklanmadı")
	}
}

func TestPublishNoTrustKeyFailClosed(t *testing.T) {
	_, priv, _ := generate(t)
	st := newFakeStore()
	svc, err := NewService(st, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	raw := signedUpdateManifest(t, priv, nil)
	if _, err := svc.Publish(context.Background(), "admin", raw); err == nil || err.Error() != ErrNoTrustKey.Error() {
		t.Fatalf("ErrNoTrustKey beklenir: %v", err)
	}
}

func generate(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, *fakeStore) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	return pub, priv, newFakeStore()
}

func TestPublishBadSignatureAndDuplicate(t *testing.T) {
	svc, _, priv := newService(t)
	raw := signedUpdateManifest(t, priv, nil)
	if _, err := svc.Publish(context.Background(), "admin", raw); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Aynı hedef+sürüm → ErrDuplicate.
	if _, err := svc.Publish(context.Background(), "admin", raw); err != ErrDuplicate {
		t.Fatalf("ErrDuplicate beklenir: %v", err)
	}
	// İmza bozulmuş manifest → ErrBadSignature.
	tampered := []byte(strings.Replace(string(raw), "0.16.0-rc1", "0.16.0-rc2", 1))
	if _, err := svc.Publish(context.Background(), "admin", tampered); err != ErrBadSignature {
		t.Fatalf("ErrBadSignature beklenir: %v", err)
	}
}

func TestStageableContract(t *testing.T) {
	svc, st, priv := newService(t)
	raw := signedUpdateManifest(t, priv, nil)
	a, err := svc.Publish(context.Background(), "admin", raw)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := svc.Stageable(context.Background(), a.ID, a.Version, a.SHA256, a.Signature); err != nil {
		t.Fatalf("Stageable: %v", err)
	}
	// Metadata uyuşmazlığı red.
	if err := svc.Stageable(context.Background(), a.ID, "9.9.9", a.SHA256, a.Signature); err == nil {
		t.Fatalf("uyuşmayan metadata reddedilmeli")
	}
	// Revoked sonrası red.
	if err := svc.Revoke(context.Background(), a.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := svc.Stageable(context.Background(), a.ID, a.Version, a.SHA256, a.Signature); err != ErrRevoked {
		t.Fatalf("ErrRevoked beklenir: %v", err)
	}
	_ = st
}

func TestParseManifestRejectsUnknownTargetAndFields(t *testing.T) {
	svc, _, priv := newService(t)
	_ = svc
	raw := signedUpdateManifest(t, priv, func(m *Manifest) { m.Target = "agent_amiga" })
	if _, _, err := ParseManifest(raw); err == nil {
		t.Fatalf("bilinmeyen hedef reddedilmeli")
	}
	raw = signedUpdateManifest(t, priv, nil)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	obj["sneaky_field"] = json.RawMessage(`true`)
	raw2, _ := json.Marshal(obj)
	if _, _, err := ParseManifest(raw2); err == nil {
		t.Fatalf("bilinmeyen alan reddedilmeli")
	}
}
