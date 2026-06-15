package srog

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns a random 128-bit identifier as a 32-character hex string,
// suitable as a request/correlation ID. It is safe for concurrent use.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand essentially never fails; degrade to a fixed marker rather
		// than panicking inside a logging path.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
