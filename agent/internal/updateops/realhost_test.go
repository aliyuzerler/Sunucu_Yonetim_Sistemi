//go:build linux

// Real-host update lifecycle (EXPECTED_UPDATE=1): full verify → stage →
// health-check → swap → rollback chain against the REAL filesystem with a
// real ed25519 key pair and REAL script binaries. Skipped silently is
// impossible: the EXPECTED_BACKUP-style gate fails the run when unset
// (CI-17 lesson — expected jobs cannot be skipped quietly).
package updateops

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealUpdateLifecycle(t *testing.T) {
	if os.Getenv("EXPECTED_UPDATE") != "1" {
		t.Skip("EXPECTED_UPDATE=1 yok — bu test CI'da ve bağımsız testte ZORUNLU koşar (sessiz atlanamaz)")
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
	oldBin := fakeBin("server-agent version 0.15.0")
	if err := os.WriteFile(target, oldBin, 0o755); err != nil {
		t.Fatalf("aktif ikilik: %v", err)
	}
	r := NewRunner([]ed25519.PublicKey{pub})
	r.IncomingDir, r.StagedDir, r.RollbackDir, r.TargetBin = inc, stg, rb, target

	// Yeni sürüm artifact'ı: gerçek imzalı içerik.
	name := "server-control-agent_0.16.0-rc1_amd64.bin"
	newBin := fakeBin("server-agent version 0.16.0-rc1")
	if err := os.WriteFile(filepath.Join(inc, name), newBin, 0o755); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	digest := sha256Hex(newBin)
	canonical, err := canonicalArtifact(name, int64(len(newBin)), digest)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))

	// 1) Stage: verify → health → swap → post-swap health.
	res, err := r.Stage(context.Background(), StagePayload{
		UpdateID: "11111111-2222-4333-8444-555555555555", Version: "0.16.0-rc1",
		SHA256: digest, Signature: sig,
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if res.State != "staged" || res.HealthCheck != "ok" {
		t.Fatalf("stage sonucu: %+v", res)
	}
	out, err := r.healthCheck(target)
	if err != nil || !strings.Contains(out, "0.16.0-rc1") {
		t.Fatalf("post-swap: %q %v", out, err)
	}

	// 2) Status kanıtı.
	st, err := r.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Staged || !st.RollbackReady || st.IncomingFiles != 0 {
		t.Fatalf("status: %+v", st)
	}

	// 3) Rollback: eski sürüme dönüş + health.
	rbRes, err := r.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rbRes.State != "rolled_back" || rbRes.HealthCheck != "ok" {
		t.Fatalf("rollback: %+v", rbRes)
	}
	out, err = r.healthCheck(target)
	if err != nil || !strings.Contains(out, "0.15.0") {
		t.Fatalf("post-rollback: %q %v", out, err)
	}

	// 4) Bozuk imza kanıtı: swap OLMAZ, aktif ikilik korunur.
	bad := fakeBin("server-agent version 9.9.9")
	if err := os.WriteFile(filepath.Join(inc, name), bad, 0o755); err != nil {
		t.Fatalf("artifact2: %v", err)
	}
	badDigest := sha256Hex(bad)
	badCanonical, _ := canonicalArtifact(name, int64(len(bad)), badDigest)
	badSig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, badCanonical))
	// İçeriği imzadan SONRA değiştir (hash uyuşmazlığı).
	if err := os.WriteFile(filepath.Join(inc, name), append(bad, []byte("tamper")...), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := r.Stage(context.Background(), StagePayload{
		UpdateID: "11111111-2222-4333-8444-555555555555", Version: "9.9.9",
		SHA256: badDigest, Signature: badSig,
	}); err != ErrHashMismatch {
		t.Fatalf("bozuk artifact reddedilmedi: %v", err)
	}
	out, _ = r.healthCheck(target)
	if !strings.Contains(out, "0.15.0") {
		t.Fatalf("reddedilen update hedefi değiştirdi: %q", out)
	}
}
