package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Token is a generated secret with its storage-safe SHA-256 hash.
type Token struct {
	Plain string // shown/returned exactly once
	Hash  string // stored in the database (SHA-256 hex)
}

// NewToken generates a 256-bit URL-safe token and its hash.
func NewToken() (Token, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, fmt.Errorf("token üretilemedi: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)
	return Token{Plain: plain, Hash: HashToken(plain)}, nil
}

// HashToken derives the SHA-256 hex digest of a token. Lookup and comparison
// always happen against this digest, never the plaintext.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// csrfDerivationPrefix domain-separates CSRF tokens from session token
// digests: sha256(prefix || sessionToken).
const csrfDerivationPrefix = "srvctl-csrf:v1:"

// DeriveCSRF derives the per-session CSRF token from the session token
// plaintext. It is never stored: the server recomputes it from the presented
// cookie. A cross-site attacker cannot read cross-origin responses, so they
// cannot obtain this value to place in X-CSRF-Token.
func DeriveCSRF(sessionToken string) string {
	sum := sha256.Sum256([]byte(csrfDerivationPrefix + sessionToken))
	return hex.EncodeToString(sum[:])
}
