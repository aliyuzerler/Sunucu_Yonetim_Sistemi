package updateops

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv builds a hermetic Runner with temp dirs, one trust key and a fake
// "binary" (shell script) whose --version output is versioned.
type testEnv struct {
	runner   *Runner
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	incoming string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("/bin/sh yok")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	root := t.TempDir()
	inc, stg, rb := filepath.Join(root, "incoming"), filepath.Join(root, "staged"), filepath.Join(root, "rollback")
	for _, d := range []string{inc, stg, rb} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("dizin: %v", err)
		}
	}
	target := filepath.Join(root, "bin", "server-agent")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("bin: %v", err)
	}
	// Mevcut "aktif" ikilik: v0.15.0.
	if err := os.WriteFile(target, fakeBin("server-agent version 0.15.0"), 0o755); err != nil {
		t.Fatalf("aktif ikilik: %v", err)
	}
	r := &Runner{Trust: []ed25519.PublicKey{pub}, IncomingDir: inc, StagedDir: stg, RollbackDir: rb, TargetBin: target}
	return &testEnv{runner: r, pub: pub, priv: priv, incoming: inc}
}

func fakeBin(version string) []byte {
	return []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"" + version + "\"; exit 0; fi\nexit 0\n")
}

func (e *testEnv) dropArtifact(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.incoming, name), content, 0o755); err != nil {
		t.Fatalf("artifact: %v", err)
	}
}

// signedMeta computes sha256 + SCP-SIG-V1 signature for the artifact content.
func (e *testEnv) signedMeta(t *testing.T, name string, content []byte) (updateID, version, digest, sig string) {
	t.Helper()
	canonical, err := canonicalArtifact(name, int64(len(content)), sha256Hex(content))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	s := ed25519.Sign(e.priv, canonical)
	return "11111111-2222-4333-8444-555555555555", "0.16.0-rc1", sha256Hex(content), base64.StdEncoding.EncodeToString(s)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

func TestStageHappyPathAndRollback(t *testing.T) {
	env := newEnv(t)
	name := "server-control-agent_0.16.0-rc1_amd64.bin"
	content := fakeBin("server-agent version 0.16.0-rc1")
	env.dropArtifact(t, name, content)
	uid, ver, digest, sig := env.signedMeta(t, name, content)

	res, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: sig})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if res.State != "staged" || res.HealthCheck != "ok" {
		t.Fatalf("beklenmeyen sonuç: %+v", res)
	}
	// Aktif ikilik artık yeni sürümü raporlamalı.
	out, err := env.runner.healthCheck(env.runner.target())
	if err != nil || !strings.Contains(out, "0.16.0-rc1") {
		t.Fatalf("aktif ikilik güncellenmedi: %q %v", out, err)
	}
	// Rollback kopyası hazır olmalı.
	st, err := env.runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.RollbackReady || !st.Staged {
		t.Fatalf("staged+rollback beklenir: %+v", st)
	}
	if st.ActiveVersion != "server-agent version 0.16.0-rc1" {
		t.Fatalf("aktif sürüm: %q", st.ActiveVersion)
	}
	// Rollback eski sürüme dönmeli.
	rb, err := env.runner.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rb.State != "rolled_back" || rb.HealthCheck != "ok" {
		t.Fatalf("rollback sonucu: %+v", rb)
	}
	out, _ = env.runner.healthCheck(env.runner.target())
	if !strings.Contains(out, "0.15.0") {
		t.Fatalf("geri alma sonrası sürüm: %q", out)
	}
	// Tüketilen artifact: incoming boşalmalı.
	entries, _ := os.ReadDir(env.incoming)
	if len(entries) != 0 {
		t.Fatalf("incoming tüketilmedi: %d dosya", len(entries))
	}
}

func TestStageRejectsUnsignedAndTampered(t *testing.T) {
	env := newEnv(t)
	name := "agent.bin"
	content := fakeBin("server-agent version 0.16.0-rc1")

	// 1) İmzasız (sahte imza).
	env.dropArtifact(t, name, content)
	uid, ver, digest, _ := env.signedMeta(t, name, content)
	fakeSig := base64.StdEncoding.EncodeToString(make([]byte, 64))
	if _, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: fakeSig}); err == nil {
		t.Fatalf("imzasız artifact reddedilmedi (kriter 2)")
	}
	// 2) Hash uyuşmazlığı (farklı içerik, geçerli imza).
	other := fakeBin("server-agent version 9.9.9")
	env.dropArtifact(t, name, other)
	_, _, _, goodSig := env.signedMeta(t, name, content) // imza content'e ait
	if _, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: goodSig}); err != ErrHashMismatch {
		t.Fatalf("hash uyuşmazlığı beklenir: %v", err)
	}
	// 3) Geçerli imza + geçerli hash ama YANLIŞ anahtar (başka env).
	env2 := newEnv(t)
	env2.dropArtifact(t, name, content)
	if _, err := env2.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: goodSig}); err != ErrBadSignature {
		t.Fatalf("yanlış güven kökü reddedilmedi: %v", err)
	}
	// Hedef ikilik hiç değişmemiş olmalı.
	out, _ := env2.runner.healthCheck(env2.runner.target())
	if !strings.Contains(out, "0.15.0") {
		t.Fatalf("reddedilen artifact hedefi değiştirdi: %q", out)
	}
}

func TestStageNoTrustKeyFailClosed(t *testing.T) {
	env := newEnv(t)
	env.runner.Trust = nil
	name := "agent.bin"
	content := fakeBin("server-agent version 0.16.0-rc1")
	env.dropArtifact(t, name, content)
	uid, ver, digest, sig := env.signedMeta(t, name, content)
	if _, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: sig}); err != ErrTrustKeyMissing {
		t.Fatalf("ErrTrustKeyMissing beklenir: %v", err)
	}
}

func TestStageAmbiguousAndMissingArtifact(t *testing.T) {
	env := newEnv(t)
	content := fakeBin("server-agent version 0.16.0-rc1")
	uid, ver, digest, sig := env.signedMeta(t, "a.bin", content)
	// Hiç artifact yok.
	if _, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: sig}); err != ErrNoArtifact {
		t.Fatalf("ErrNoArtifact beklenir: %v", err)
	}
	// İki dosya → belirsizlik tipli red.
	env.dropArtifact(t, "a.bin", content)
	env.dropArtifact(t, "b.bin", content)
	if _, err := env.runner.Stage(context.Background(), StagePayload{UpdateID: uid, Version: ver, SHA256: digest, Signature: sig}); err != ErrAmbiguousInput {
		t.Fatalf("ErrAmbiguousInput beklenir: %v", err)
	}
}

func TestStageHealthFailAutoRollback(t *testing.T) {
	env := newEnv(t)
	// Health-check'i BOZUK yeni ikilik: --version'da 1 ile çıkar.
	name := "agent.bin"
	broken := []byte("#!/bin/sh\nexit 3\n")
	env.dropArtifact(t, name, broken)
	canonical, err := canonicalArtifact(name, int64(len(broken)), sha256Hex(broken))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig := ed25519.Sign(env.priv, canonical)
	if _, err := env.runner.Stage(context.Background(), StagePayload{
		UpdateID:  "11111111-2222-4333-8444-555555555555",
		Version:   "0.16.0-rc1",
		SHA256:    sha256Hex(broken),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}); err == nil || !strings.Contains(err.Error(), "health-check") {
		t.Fatalf("health-check hatası beklenir: %v", err)
	}
	// Otomatik geri alma: aktif ikilik eski sürümde kalmalı.
	out, _ := env.runner.healthCheck(env.runner.target())
	if !strings.Contains(out, "0.15.0") {
		t.Fatalf("otomatik geri alma uygulanmadı: %q", out)
	}
}

func TestRollbackWithoutCopy(t *testing.T) {
	env := newEnv(t)
	if _, err := env.runner.Rollback(context.Background()); err != ErrNoRollback {
		t.Fatalf("ErrNoRollback beklenir: %v", err)
	}
}

func TestValidateStageSchema(t *testing.T) {
	if err := ValidateStage(StagePayload{UpdateID: "x", Version: "1.0.0", SHA256: "ab", Signature: "cd"}); err == nil {
		t.Fatalf("bozuk payload reddedilmeli")
	}
	// JSON round-trip alan adları (mirror sözleşmesi).
	b, _ := json.Marshal(StagePayload{UpdateID: "11111111-2222-4333-8444-555555555555", Version: "0.16.0-rc1", SHA256: strings.Repeat("ab", 32), Signature: strings.Repeat("A", 88)})
	var p map[string]any
	_ = json.Unmarshal(b, &p)
	for _, k := range []string{"update_id", "version", "sha256", "signature"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("alan eksik: %s", k)
		}
	}
}

// Contract vector: SCP-SIG-V1 canonical bytes must stay byte-identical to
// backend/control-plane/internal/signpkg. The SAME vector is asserted there.
func TestCanonicalArtifactVector(t *testing.T) {
	got, err := canonicalArtifact("agent.deb", 12, "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := "scp-sign-v1\nagent.deb\n12\nabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n"
	if string(got) != want {
		t.Fatalf("kanonik vektör sapması:\n%q\n%q", got, want)
	}
}
