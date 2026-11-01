// Package agent implements the node agent: local key/cert storage, CSR-based
// enrollment and the outbound mTLS stream loop (ADR-0008).
//
// Security invariants:
//   - The private key is generated HERE and never leaves this process or node.
//   - Only the public CSR travels to the control plane.
//   - Certificates are stored 0600 in the data directory.
package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotEnrolled is returned when run is attempted without a certificate.
var ErrNotEnrolled = errors.New("agent kayıtlı değil: önce 'server-agent enroll' çalıştırın")

// Store keeps the agent's key material in a data directory.
type Store struct {
	Dir string
}

// KeyPath / CertPath / CAPath are the file locations inside Dir.
func (s *Store) KeyPath() string  { return filepath.Join(s.Dir, "agent.key") }
func (s *Store) CertPath() string { return filepath.Join(s.Dir, "agent.crt") }
func (s *Store) CAPath() string   { return filepath.Join(s.Dir, "ca.crt") }

// EnsureDir creates the data directory with restrictive permissions.
func (s *Store) EnsureDir() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("veri dizini: %w", err)
	}
	return nil
}

// HasIdentity reports whether key+cert exist.
func (s *Store) HasIdentity() bool {
	return fileExists(s.KeyPath()) && fileExists(s.CertPath())
}

// GenerateKey creates (or replaces) the agent private key, 0600.
func (s *Store) GenerateKey() (*ecdsa.PrivateKey, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("anahtar üretilemedi: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	// 0600: özel anahtar yalnızca sahibi tarafından okunabilir.
	if err := os.WriteFile(s.KeyPath(), pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("anahtar yazılamadı: %w", err)
	}
	return key, nil
}

// LoadKey reads the persisted private key.
func (s *Store) LoadKey() (*ecdsa.PrivateKey, error) {
	pemBytes, err := os.ReadFile(s.KeyPath())
	if err != nil {
		return nil, ErrNotEnrolled
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("anahtar PEM'i geçersiz")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// BuildCSR creates the PKCS#10 CSR signed with the given key. No SAN identity
// is requested: the SERVER assigns the immutable node ID.
func BuildCSR(key *ecdsa.PrivateKey) (string, error) {
	tpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "server-agent", Organization: []string{"Server Control Nodes"}},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		return "", fmt.Errorf("CSR üretilemedi: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

// SaveCert persists the issued certificate and the CA certificate (public
// material; 0644 is acceptable — the PRIVATE key above is the secret).
func (s *Store) SaveCert(certPEM, caPEM string) error {
	if err := s.EnsureDir(); err != nil {
		return err
	}
	if err := os.WriteFile(s.CertPath(), []byte(certPEM), 0o644); err != nil {
		return fmt.Errorf("sertifika yazılamadı: %w", err)
	}
	if err := os.WriteFile(s.CAPath(), []byte(caPEM), 0o644); err != nil {
		return fmt.Errorf("CA yazılamadı: %w", err)
	}
	return nil
}

// tlsConfigParts holds the PEM parts needed for the mTLS client config.
type tlsConfigParts struct {
	keyPEM  []byte
	certPEM []byte
	caPEM   []byte
}

// LoadTLSConfigParts reads key/cert/ca PEMs from the store.
func (s *Store) LoadTLSConfigParts() (*tlsConfigParts, error) {
	key, err := os.ReadFile(s.KeyPath())
	if err != nil {
		return nil, ErrNotEnrolled
	}
	cert, err := os.ReadFile(s.CertPath())
	if err != nil {
		return nil, ErrNotEnrolled
	}
	ca, err := os.ReadFile(s.CAPath())
	if err != nil {
		return nil, fmt.Errorf("CA dosyası okunamadı: %w", err)
	}
	return &tlsConfigParts{keyPEM: key, certPEM: cert, caPEM: ca}, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
