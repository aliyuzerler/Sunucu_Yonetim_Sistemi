package broker

import (
	"crypto/rand"
	"encoding/hex"
)

// cryptoRandRead fills b with cryptographic randomness (test+prod shared).
func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }

// hexEncode hex-encodes bytes.
func hexEncode(b []byte) string { return hex.EncodeToString(b) }
