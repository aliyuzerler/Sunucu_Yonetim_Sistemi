// Package certs implements the control plane certificate authority (CA) and
// node-identity certificate issuance for the agent mTLS protocol (ADR-0008).
//
// The CA key is an operator-provisioned file on the control plane host
// (never stored in PostgreSQL, never transmitted). Agents generate their own
// private keys and submit CSRs; only public-key material ever reaches the
// server. Issued certificates bind the immutable node ID via URI SAN
// "urn:servercontrol:node:<uuid>".
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Errors mapped by callers to stable API/gRPC responses.
var (
	ErrInvalidCSR  = errors.New("CSR geçersiz veya imzası doğrulanamıyor")
	ErrCANotLoaded = errors.New("CA yapılandırılmamış")
	ErrWeakCSRKey  = errors.New("CSR ortak anahtarı desteklenmiyor (ECDSA P-256/P-384 veya RSA >= 2048)")
)

// SANOpaquePrefix is the identity URI SAN prefix: "urn:servercontrol:node:<id>".
const SANOpaquePrefix = "servercontrol:node:"

// CA holds the parsed issuing certificate and private key.
type CA struct {
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// LeafCert is an issued node certificate with metadata persisted separately
// from the PEM (the PEM never contains private key material).
type LeafCert struct {
	CertPEM     string
	Serial      string
	Fingerprint string // lowercase hex sha256 of DER
	NotBefore   time.Time
	NotAfter    time.Time
	NodeID      string
}

// GenerateCA creates a new self-signed ECDSA P-256 CA and writes cert/key
// files (key 0600). Used by `control-plane ca init`; production operators may
// provision their own CA instead.
func GenerateCA(certPath, keyPath, commonName string) error {
	if commonName == "" {
		commonName = "Server Control Root CA"
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("CA anahtarı üretilemedi: %w", err)
	}
	serial, err := randSerial()
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Server Control"}},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("CA sertifikası üretilemedi: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("CA anahtarı kodlanamadı: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return fmt.Errorf("CA dizini: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("CA sertifikası yazılamadı: %w", err)
	}
	// 0600: özel anahtar dosyası yalnızca sahibi tarafından okunabilir.
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("CA anahtarı yazılamadı: %w", err)
	}
	return nil
}

// LoadCA reads and validates the CA certificate/key pair from disk.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("%w: CA sertifikası okunamadı: %v", ErrCANotLoaded, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: CA anahtarı okunamadı: %v", ErrCANotLoaded, err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: CA sertifika PEM'i geçersiz", ErrCANotLoaded)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: CA sertifikası çözümlenemedi: %v", ErrCANotLoaded, err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("%w: CA anahtar PEM'i geçersiz", ErrCANotLoaded)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: CA anahtarı çözümlenemedi: %v", ErrCANotLoaded, err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("%w: verilen sertifika CA değil", ErrCANotLoaded)
	}
	return &CA{Cert: cert, key: key}, nil
}

// CertificatePEM returns the CA certificate as PEM (public material).
func (c *CA) CertificatePEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Cert.Raw}))
}

// IssueNodeCert validates the CSR and signs a node-identity leaf certificate.
// The CSR signature must verify against its own public key with a supported
// algorithm. The node ID is embedded by the SERVER as the URI SAN; the agent
// cannot choose its identity (any SAN in the CSR is ignored).
func (c *CA) IssueNodeCert(csrPEM []byte, nodeID string, ttl time.Duration) (*LeafCert, error) {
	if c == nil {
		return nil, ErrCANotLoaded
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, ErrInvalidCSR
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCSR, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: imza doğrulanamadı: %v", ErrInvalidCSR, err)
	}
	if err := validatePublicKey(csr.PublicKey); err != nil {
		return nil, err
	}
	if ttl <= 0 || ttl > 365*24*time.Hour {
		ttl = 90 * 24 * time.Hour
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now().Add(-1 * time.Minute)
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: nodeID, Organization: []string{"Server Control Nodes"}},
		NotBefore:    now,
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{{Scheme: "urn", Opaque: SANOpaquePrefix + nodeID}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("sertifika imzalanamadı: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("sertifika doğrulanamadı: %w", err)
	}
	return &LeafCert{
		CertPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Serial:      serial.Text(16),
		Fingerprint: Fingerprint(parsed),
		NotBefore:   parsed.NotBefore,
		NotAfter:    parsed.NotAfter,
		NodeID:      nodeID,
	}, nil
}

// Fingerprint returns the lowercase hex SHA-256 of the certificate DER.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// NodeIDFromSANs extracts the node ID from the identity URI SAN
// "urn:servercontrol:node:<uuid>"; returns an error when missing.
func NodeIDFromSANs(uris []*url.URL) (string, error) {
	for _, u := range uris {
		if u == nil {
			continue
		}
		if u.Scheme == "urn" && len(u.Opaque) > len(SANOpaquePrefix) && u.Opaque[:len(SANOpaquePrefix)] == SANOpaquePrefix {
			return u.Opaque[len(SANOpaquePrefix):], nil
		}
	}
	return "", errors.New("node kimliği URI SAN'ı yok (urn:servercontrol:node:<id>)")
}

func validatePublicKey(pub any) error {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve == elliptic.P256() || k.Curve == elliptic.P384() {
			return nil
		}
		return ErrWeakCSRKey
	case *rsa.PublicKey:
		if k.N.BitLen() >= 2048 {
			return nil
		}
		return ErrWeakCSRKey
	default:
		return ErrWeakCSRKey
	}
}

func randSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	s, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("seri numarası üretilemedi: %w", err)
	}
	return s, nil
}

// IssueServerCert signs a TLS SERVER certificate for the control plane
// listener. DNS/IP SANs are required so agents can verify the endpoint; the
// certificate is held in memory (regenerated at startup).
func (c *CA) IssueServerCert(dnsNames []string, ipAddresses []string, ttl time.Duration) (tlsCertificate tls.Certificate, err error) {
	if c == nil {
		return tls.Certificate{}, ErrCANotLoaded
	}
	if ttl <= 0 || ttl > 365*24*time.Hour {
		ttl = 90 * 24 * time.Hour
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("sunucu anahtarı üretilemedi: %w", err)
	}
	serial, err := randSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	ips := make([]net.IP, 0, len(ipAddresses))
	for _, s := range ipAddresses {
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		}
	}
	now := time.Now().Add(-1 * time.Minute)
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "server-control-plane", Organization: []string{"Server Control"}},
		NotBefore:    now,
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, &key.PublicKey, c.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("sunucu sertifikası imzalanamadı: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("sunucu sertifikası çözümlenemedi: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der, c.Cert.Raw},
		PrivateKey:  key,
		Leaf:        cert,
	}, nil
}
