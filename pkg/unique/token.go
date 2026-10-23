package unique

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// NewToken returns a cryptographically random 128-bit token encoded as hex.
func NewToken() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
