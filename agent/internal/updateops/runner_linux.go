//go:build linux

package updateops

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes update.* operations with node-managed paths. All knobs
// (paths, trust keys) come from the BROKER configuration — never payloads.
type Runner struct {
	// Trust holds the accepted ed25519 public keys (--update-trust-key,
	// repeatable). Empty → every op fails closed with ErrTrustKeyMissing.
	Trust []ed25519.PublicKey

	// HealthTimeout bounds one `--version` health check execution.
	HealthTimeout time.Duration

	// Overridable roots for hermetic tests (production values are the
	// package constants; tests point them at t.TempDir()).
	IncomingDir string
	StagedDir   string
	RollbackDir string
	TargetBin   string
}

// NewRunner builds the production runner with the given trust keys.
func NewRunner(trust []ed25519.PublicKey) *Runner {
	return &Runner{Trust: trust, HealthTimeout: 10 * time.Second}
}

func (r *Runner) incoming() string {
	if r.IncomingDir != "" {
		return r.IncomingDir
	}
	return IncomingDir
}

func (r *Runner) staged() string {
	if r.StagedDir != "" {
		return r.StagedDir
	}
	return StagedDir
}

func (r *Runner) rollback() string {
	if r.RollbackDir != "" {
		return r.RollbackDir
	}
	return RollbackDir
}

func (r *Runner) target() string {
	if r.TargetBin != "" {
		return r.TargetBin
	}
	return TargetBin
}

// StageResult reports the staging outcome (kriter 2: doğrulanmış staging +
// health-check kanıtı).
type StageResult struct {
	State       string `json:"state"` // staged
	Version     string `json:"version"`
	Artifact    string `json:"artifact_name"`
	SHA256      string `json:"sha256"`
	HealthCheck string `json:"health_check"` // ok | failed
	HealthOut   string `json:"health_output,omitempty"`
}

// StatusResult reports current update state (read-only).
type StatusResult struct {
	ActiveVersion string `json:"active_version,omitempty"`
	Staged        bool   `json:"staged"`
	StagedVersion string `json:"staged_version,omitempty"`
	RollbackReady bool   `json:"rollback_ready"`
	IncomingFiles int    `json:"incoming_files"`
	UpdateEnabled bool   `json:"update_enabled"`
}

// RollbackResult reports the rollback outcome.
type RollbackResult struct {
	State       string `json:"state"` // rolled_back
	HealthCheck string `json:"health_check"`
	HealthOut   string `json:"health_output,omitempty"`
}

// healthCheck runs `<binary> --version` and returns trimmed output; a
// non-zero exit or timeout is an error.
func (r *Runner) healthCheck(bin string) (string, error) {
	timeout := r.HealthTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("health-check: %w", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", fmt.Errorf("health-check: çıktı boş")
	}
	if len(text) > 256 {
		text = text[:256]
	}
	return text, nil
}

// singleIncoming requires EXACTLY one regular file in the incoming dir
// (ambiguity is a typed refusal — never "pick the first").
func (r *Runner) singleIncoming() (string, error) {
	entries, err := os.ReadDir(r.incoming())
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoArtifact
		}
		return "", err
	}
	found := ""
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !nameRE.MatchString(e.Name()) || strings.Contains(e.Name(), "..") {
			continue // never consider unsafe names
		}
		found = filepath.Join(r.incoming(), e.Name())
		count++
	}
	switch {
	case count == 0:
		return "", ErrNoArtifact
	case count > 1:
		return "", ErrAmbiguousInput
	}
	return found, nil
}

// Stage verifies (sha256 + signature), health-checks the staged binary,
// swaps it into the target path keeping a rollback copy, and re-checks
// health AFTER the swap (auto-rollback on failure).
func (r *Runner) Stage(ctx context.Context, p StagePayload) (StageResult, error) {
	if err := ValidateStage(p); err != nil {
		return StageResult{}, err
	}
	if len(r.Trust) == 0 {
		return StageResult{}, ErrTrustKeyMissing
	}
	for _, dir := range []string{r.incoming(), r.staged(), r.rollback()} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return StageResult{}, fmt.Errorf("dizin hazırlanamadı: %w", err)
		}
	}
	artifact, err := r.singleIncoming()
	if err != nil {
		return StageResult{}, err
	}
	// 1) sha256 + signature — reject BEFORE any staging (kriter 2).
	if err := verifyArtifact(artifact, r.Trust, p); err != nil {
		return StageResult{}, err
	}
	// 2) Stage: copy into the staged dir under a deterministic name.
	stagedPath := filepath.Join(r.staged(), "staged-"+p.SHA256[:16])
	if err := copyFile(artifact, stagedPath, 0o755); err != nil {
		return StageResult{}, fmt.Errorf("staging kopyası: %w", err)
	}
	// 3) Health check the STAGED binary itself.
	out, err := r.healthCheck(stagedPath)
	if err != nil {
		_ = os.Remove(stagedPath)
		return StageResult{}, fmt.Errorf("%w: %v", ErrHealthCheck, err)
	}
	// 4) Keep the rollback copy of the current target (if any).
	if _, err := os.Stat(r.target()); err == nil {
		if err := copyFile(r.target(), filepath.Join(r.rollback(), "server-agent.prev"), 0o755); err != nil {
			return StageResult{}, fmt.Errorf("rollback kopyası: %w", err)
		}
	}
	// 5) Atomic swap: copy staged → temp next to target, rename over.
	if err := os.MkdirAll(filepath.Dir(r.target()), 0o755); err != nil {
		return StageResult{}, fmt.Errorf("hedef dizini: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.target()), ".update-")
	if err != nil {
		return StageResult{}, fmt.Errorf("geçici dosya: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	if err := copyFile(stagedPath, tmpName, 0o755); err != nil {
		return StageResult{}, fmt.Errorf("%w: %v", ErrSwapFailed, err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return StageResult{}, fmt.Errorf("%w: %v", ErrSwapFailed, err)
	}
	if err := os.Rename(tmpName, r.target()); err != nil {
		return StageResult{}, fmt.Errorf("%w: %v", ErrSwapFailed, err)
	}
	// 6) Health check AFTER the swap; auto-rollback on failure.
	out2, err := r.healthCheck(r.target())
	if err != nil {
		if rbErr := r.restoreRollback(); rbErr != nil {
			return StageResult{}, fmt.Errorf("%w (geri alma da başarısız: %v): %v", ErrHealthCheck, rbErr, err)
		}
		return StageResult{}, fmt.Errorf("%w: %v", ErrHealthCheck, err)
	}
	// 7) Consume the incoming artifact (verified) and keep the staged copy
	// for status reporting.
	_ = os.Remove(artifact)
	return StageResult{
		State: "staged", Version: p.Version,
		Artifact:    baseName(stagedPath),
		SHA256:      p.SHA256,
		HealthCheck: "ok", HealthOut: out + " | post-swap: " + out2,
	}, nil
}

// Status reports active/staged/rollback/incoming state (read-only).
func (r *Runner) Status(ctx context.Context) (StatusResult, error) {
	res := StatusResult{UpdateEnabled: len(r.Trust) > 0}
	if out, err := r.healthCheck(r.target()); err == nil {
		res.ActiveVersion = out
	}
	entries, err := os.ReadDir(r.staged())
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && stagedRE.MatchString(e.Name()) {
				res.Staged = true
			}
		}
	}
	if _, err := os.Stat(filepath.Join(r.rollback(), "server-agent.prev")); err == nil {
		res.RollbackReady = true
	}
	if inc, err := os.ReadDir(r.incoming()); err == nil {
		n := 0
		for _, e := range inc {
			if !e.IsDir() {
				n++
			}
		}
		res.IncomingFiles = n
	}
	return res, nil
}

// Rollback restores the previous binary and health-checks it.
func (r *Runner) Rollback(ctx context.Context) (RollbackResult, error) {
	prev := filepath.Join(r.rollback(), "server-agent.prev")
	if _, err := os.Stat(prev); err != nil {
		return RollbackResult{}, ErrNoRollback
	}
	if err := r.restoreRollback(); err != nil {
		return RollbackResult{}, err
	}
	out, err := r.healthCheck(r.target())
	if err != nil {
		return RollbackResult{}, fmt.Errorf("%w: %v", ErrHealthCheck, err)
	}
	return RollbackResult{State: "rolled_back", HealthCheck: "ok", HealthOut: out}, nil
}

func (r *Runner) restoreRollback() error {
	prev := filepath.Join(r.rollback(), "server-agent.prev")
	tmp, err := os.CreateTemp(filepath.Dir(r.target()), ".rollback-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	if err := copyFile(prev, tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, r.target())
}
