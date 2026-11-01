//go:build unix

package tlsops

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner executes the TLS certificate operations for real (broker-only;
// root). Every external command is a fixed binary + argv (never a shell).
// Injectables keep broker tests hermetic.
type Runner struct {
	CertDir   string
	NginxBin  string
	Systemctl string

	// Injectables (hermetic tests; nil = real implementations).
	execFn   func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath func(string) (string, error)
	nowFn    func() time.Time
}

// NewRunner builds a Runner with the default system layout.
func NewRunner() *Runner {
	return &Runner{
		CertDir:   DefaultCertDir,
		NginxBin:  DefaultNginxBin,
		Systemctl: DefaultSystemctl,
	}
}

func (r *Runner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.execFn != nil {
		return r.execFn(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		trimmed := strings.TrimSpace(out.String())
		if len(trimmed) > 4000 {
			trimmed = trimmed[:4000]
		}
		if trimmed != "" {
			return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, trimmed)
		}
		return out.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}

func (r *Runner) now() time.Time {
	if r.nowFn != nil {
		return r.nowFn()
	}
	return time.Now()
}

// nginxAvailable verifies the nginx binary exists (typed refusal).
func (r *Runner) nginxAvailable() error {
	bin := r.NginxBin
	if r.lookPath != nil {
		if _, err := r.lookPath(bin); err != nil {
			return fmt.Errorf("%w: nginx bulunamadı (%s)", ErrUnavailable, bin)
		}
		return nil
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: nginx bulunamadı (%s)", ErrUnavailable, bin)
	}
	return nil
}

// paths derives the per-domain file layout. The domain is the ONLY user
// input and is strictly validated before reaching here.
func certPaths(certDir, domain string) (dir, key, cert string) {
	dir = filepath.Join(certDir, domain)
	return dir, filepath.Join(dir, KeyFile), filepath.Join(dir, CertFile)
}

// Request generates (or reuses) the domain private key and returns the CSR.
// The key file is 0600; the CSR is public material and is returned to the
// caller (task result) — it never contains the private key.
func (r *Runner) Request(ctx context.Context, domain string, sans []string) (RequestResult, error) {
	domains, err := NormalizeDomains(domain, sans)
	if err != nil {
		return RequestResult{}, err
	}
	dir, keyPath, _ := certPaths(r.CertDir, domains[0])

	var key *ecdsa.PrivateKey
	reused := false
	if raw, err := os.ReadFile(keyPath); err == nil {
		key, err = loadECKey(raw)
		if err != nil {
			return RequestResult{}, fmt.Errorf("%w: mevcut anahtar okunamadı: %v", ErrCertInvalid, err)
		}
		reused = true
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return RequestResult{}, fmt.Errorf("anahtar üretilemedi: %w", err)
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return RequestResult{}, err
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return RequestResult{}, fmt.Errorf("sertifika dizini açılamadı: %w", err)
		}
		// Atomik yazım + 0600: anahtar asla geniş okunur olmaz.
		tmp := keyPath + TempSuffix
		if err := os.WriteFile(tmp, pemBytes, 0o600); err != nil {
			return RequestResult{}, fmt.Errorf("anahtar yazılamadı: %w", err)
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			_ = os.Remove(tmp)
			return RequestResult{}, err
		}
		if err := os.Rename(tmp, keyPath); err != nil {
			_ = os.Remove(tmp)
			return RequestResult{}, fmt.Errorf("anahtar konumlandırılamadı: %w", err)
		}
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: domains[0]},
		DNSNames:           domains,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}, key)
	if err != nil {
		return RequestResult{}, fmt.Errorf("CSR üretilemedi: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return RequestResult{}, err
	}
	return RequestResult{
		Domain:         domains[0],
		SANs:           domains,
		CSRPEM:         string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		KeyFingerprint: FingerprintOf(pubDER),
		KeyReused:      reused,
	}, nil
}

// Install validates public material against the on-disk key, stages it,
// swaps atomically with a rollback copy, runs the `nginx -t` gate and
// reloads ONLY after the gate passes. Any failure restores the previous
// chain — the live certificate is never broken (criterion 4).
func (r *Runner) Install(ctx context.Context, domain, certPEM, chainPEM string) (InstallResult, error) {
	if !ValidDomain(domain) {
		return InstallResult{}, fmt.Errorf("%w: %q", ErrInvalidDomain, domain)
	}
	if certPEM == "" || len(certPEM) > MaxCertPEMLen || len(chainPEM) > MaxCertPEMLen {
		return InstallResult{}, fmt.Errorf("%w: PEM boyut sınırları", ErrCertInvalid)
	}
	if err := r.nginxAvailable(); err != nil {
		return InstallResult{}, err
	}

	// ---- Doğrulama (hiçbir dosya henüz değişmedi) ----
	leaf, err := parseLeaf(certPEM)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrCertInvalid, err)
	}
	if r.now().After(leaf.NotAfter) {
		return InstallResult{}, fmt.Errorf("%w: not_after=%s", ErrCertExpired, leaf.NotAfter.Format(time.RFC3339))
	}
	if !certCoversDomain(leaf, domain) {
		return InstallResult{}, fmt.Errorf("%w: SAN %q kapsamıyor", ErrCertInvalid, domain)
	}
	if _, err := parseLeaf(chainPEM); chainPEM != "" && err != nil {
		return InstallResult{}, fmt.Errorf("%w: zincir: %v", ErrCertInvalid, err)
	}

	_, keyPath, certPath := certPaths(r.CertDir, domain)
	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return InstallResult{}, fmt.Errorf("%w: önce web.cert.request koşmalı (anahtar yok)", ErrNotFound)
		}
		return InstallResult{}, fmt.Errorf("anahtar okunamadı: %w", err)
	}
	key, err := loadECKey(keyRaw)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: anahtar: %v", ErrCertInvalid, err)
	}
	// ÇİFT EŞLEŞME: sertifika, bu node'daki anahtarla eşleşmeli.
	if !samePublicKey(leaf.PublicKey, &key.PublicKey) {
		return InstallResult{}, ErrKeyPairMismatch
	}

	fullchain := certPEM + chainPEM

	// ---- Sahne: temp yaz → rollback kopyası → atomik takas ----
	tmp := certPath + TempSuffix
	if err := os.WriteFile(tmp, []byte(fullchain), 0o644); err != nil {
		return InstallResult{}, fmt.Errorf("staging yazımı başarısız: %w", err)
	}
	var rollback string
	if prev, err := os.ReadFile(certPath); err == nil {
		rollback = certPath + RollbackSuffix
		if err := os.WriteFile(rollback, prev, 0o644); err != nil {
			_ = os.Remove(tmp)
			return InstallResult{}, fmt.Errorf("rollback kopyası yazılamadı: %w", err)
		}
	}
	if err := os.Rename(tmp, certPath); err != nil {
		_ = os.Remove(tmp)
		return InstallResult{}, fmt.Errorf("atomik takas başarısız: %w", err)
	}

	restore := func() {
		if rollback != "" {
			if prev, err := os.ReadFile(rollback); err == nil {
				_ = os.WriteFile(certPath, prev, 0o644)
			}
		} else {
			_ = os.Remove(certPath)
		}
	}

	// ---- nginx -t kapısı: geçmeden reload YOK ----
	if _, err := r.run(ctx, r.NginxBin, "-t"); err != nil {
		restore()
		_, _ = r.run(ctx, r.NginxBin, "-t") // sistem tutarlılığını geri doğrula
		return InstallResult{}, fmt.Errorf("%w: %v (önceki sertifika geri yüklendi)", ErrSyntax, err)
	}

	// ---- Reload ----
	reloaded := true
	if _, err := r.run(ctx, r.Systemctl, "reload", "nginx"); err != nil {
		// Reload başarısız olsa bile diskteki sertifika geçerli ve
		// doğrulanmış; durum dürüstçe raporlanır (rollback YAPILMAZ:
		// nginx -t geçti, config tutarlı).
		reloaded = false
	}
	return InstallResult{
		Domain:       domain,
		NotAfter:     leaf.NotAfter,
		Fingerprint:  FingerprintOf(leaf.Raw),
		Reloaded:     reloaded,
		RollbackUsed: rollback != "",
	}, nil
}

// Status lists on-disk certificates with expiry metadata.
func (r *Runner) Status(ctx context.Context) ([]CertStatus, error) {
	entries, err := os.ReadDir(r.CertDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CertStatus{}, nil
		}
		return nil, fmt.Errorf("sertifika dizini okunamadı: %w", err)
	}
	out := []CertStatus{}
	now := r.now()
	for _, e := range entries {
		if !e.IsDir() || len(out) >= MaxCertList {
			continue
		}
		domain := e.Name()
		if !ValidDomain(domain) {
			continue // yabancı dizin: raporlanmaz, asla silinmez
		}
		_, keyPath, certPath := certPaths(r.CertDir, domain)
		raw, err := os.ReadFile(certPath)
		if err != nil {
			continue // yarım kurulum: raporlanmaz
		}
		leaf, err := parseLeaf(string(raw))
		if err != nil {
			continue
		}
		_, keyErr := os.Stat(keyPath)
		days := int(leaf.NotAfter.Sub(now).Hours() / 24)
		if days < 0 {
			days = 0
		}
		out = append(out, CertStatus{
			Domain:      domain,
			NotAfter:    leaf.NotAfter,
			Fingerprint: FingerprintOf(leaf.Raw),
			KeyPresent:  keyErr == nil,
			DaysLeft:    days,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// ---- helpers ----

func parseLeaf(pemStr string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("CERTIFICATE PEM bloğu yok")
	}
	return x509.ParseCertificate(block.Bytes)
}

func certCoversDomain(leaf *x509.Certificate, domain string) bool {
	for _, n := range leaf.DNSNames {
		if strings.EqualFold(n, domain) {
			return true
		}
	}
	return strings.EqualFold(leaf.Subject.CommonName, domain)
}

func loadECKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("EC PRIVATE KEY PEM bloğu yok")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// samePublicKey compares two public keys via DER canonical form.
func samePublicKey(a crypto.PublicKey, b crypto.PublicKey) bool {
	aDER, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false
	}
	bDER, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aDER, bDER)
}
