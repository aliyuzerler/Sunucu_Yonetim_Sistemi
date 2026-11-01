// Package identity implements user accounts, password hashing, sessions,
// the one-time admin bootstrap and login rate limiting for the control plane.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow the RFC 9106 second recommended option
// (m=64 MiB, t=3, p=4 is heavy for small hosts; we use OWASP-recommended
// m=19456 KiB, t=2, p=1 which balances security and control-plane cost).
const (
	argonMemory  uint32 = 19 * 1024 // KiB (19456 rounded)
	argonTime    uint32 = 2
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// ErrInvalidCredentials is returned for wrong username/password so callers
// emit one stable, user-safe error.
var ErrInvalidCredentials = errors.New("kimlik bilgileri geçersiz")

// HashPassword derives an argon2id PHC string for the plaintext password.
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("parola uzunluğu 12..256 olmalı")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt üretilemedi: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodePHC(salt, key), nil
}

// VerifyPassword checks the plaintext password against a stored PHC string in
// constant time after decoding. Recomputation under the stored parameters is
// the constant-time comparison primitive for argon2id.
func VerifyPassword(password, phc string) (bool, error) {
	salt, want, err := decodePHC(phc)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// encodePHC renders the standard PHC string:
// $argon2id$v=19$m=19456,t=2,p=1$<salt-b64>$<hash-b64>
func encodePHC(salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

func decodePHC(phc string) (salt, key []byte, err error) {
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=19456,t=2,p=1", "salt", "hash"]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, errors.New("phc dizisi biçimi geçersiz")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, errors.New("argon2 sürümü desteklenmiyor")
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return nil, nil, errors.New("argon2 parametreleri okunamadı")
	}
	if m != argonMemory || t != argonTime || p != argonThreads {
		return nil, nil, errors.New("argon2 parametreleri bilinen yapılandırmaya ait değil")
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, errors.New("salt çözülemedi")
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, errors.New("hash çözülemedi")
	}
	if len(salt) < 8 || len(key) < 16 {
		return nil, nil, errors.New("salt/hash uzunluğu güvenli değil")
	}
	return salt, key, nil
}
