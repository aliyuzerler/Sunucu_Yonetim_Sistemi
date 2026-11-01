// Package secrets implements authenticated encryption (AES-256-GCM) for
// control-plane secrets at rest — Faz 09: external integration credentials
// (DNS provider API tokens) and issued certificate material.
//
// Security model (ADR-0013, docs/architecture/dns-ssl.md):
//   - The key NEVER lives in PostgreSQL. It comes from the environment
//     (CP_SECRETS_KEY, base64-encoded 32 bytes) — operator provisioned.
//   - Ciphertext is nonce-prefixed (12 random bytes) || AES-GCM output;
//     tampering fails with ErrCiphertextInvalid (GCM auth tag).
//   - Plaintext secrets are returned only to task executors; REST responses
//     NEVER include them (write-only UI contract).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	keyLen = 32 // AES-256
	// MaxPlaintext bounds what we are willing to encrypt (provider
	// credential JSON is a few KiB at most; cert chains ~10 KiB).
	MaxPlaintext = 64 << 10
)

var (
	ErrKeyInvalid       = errors.New("secrets: anahtar 32 bayt (base64) olmalı")
	ErrCiphertextTooBig = errors.New("secrets: düz metin sınırı aşıldı")
	ErrCiphertextShort  = errors.New("secrets: şifreli veri çok kısa")
	ErrCiphertextInval  = errors.New("secrets: şifreli veri doğrulanamadı (anahtar veya içerik farklı)")
)

// Key is a validated 32-byte AES-256 key.
type Key struct{ raw [keyLen]byte }

// NewKey builds a key from raw bytes (must be exactly 32).
func NewKey(raw []byte) (Key, error) {
	var k Key
	if len(raw) != keyLen {
		return k, ErrKeyInvalid
	}
	copy(k.raw[:], raw)
	return k, nil
}

// ParseKey decodes a base64 (std, URL-safe or raw) encoded 32-byte key.
func ParseKey(s string) (Key, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Tolerate URL-safe / unpadded encodings (operator convenience).
		if b, err = base64.RawURLEncoding.DecodeString(s); err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(s); err != nil {
				return Key{}, ErrKeyInvalid
			}
		}
	}
	return NewKey(b)
}

// Generate creates a fresh random key (tests and operator bootstrap helper).
func Generate() (Key, error) {
	raw := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return Key{}, fmt.Errorf("secrets: anahtar üretilemedi: %w", err)
	}
	return NewKey(raw)
}

// KeyNotFoundError marks "not configured" distinctly from bad input: the
// operator must provision CP_SECRETS_KEY before secrets are usable.
type KeyNotFoundError struct{}

func (KeyNotFoundError) Error() string {
	return "secrets: şifreleme anahtarı yapılandırılmadı (CP_SECRETS_KEY)"
}

func (k Key) cipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.raw[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm, nil
}

// Encrypt seals plaintext: output = nonce(12) || ciphertext+tag.
func (k Key) Encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrCiphertextTooBig
	}
	gcm, err := k.cipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce üretilemedi: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens nonce-prefixed GCM ciphertext.
func (k Key) Decrypt(sealed []byte) ([]byte, error) {
	gcm, err := k.cipher()
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrCiphertextShort
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return nil, ErrCiphertextInval
	}
	return plain, nil
}
