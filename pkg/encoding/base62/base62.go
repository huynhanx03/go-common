// Package base62 implements base62 encoding — the URL-safe alphabet
// [0-9A-Za-z] with no separator characters, so values embed cleanly in
// URLs, filenames, and codes users copy around.
//
// The main use is exposing binary IDs in a compact form: a UUID drops from
// 36 characters to 22 and decodes back losslessly, so nothing extra is
// stored:
//
//	s, err := base62.Encode(id[:]) // "1BJhYuJgAcTkzLdaqPezov"
//	b, err := base62.Decode(s)
//	id, err := uuid.FromBytes(b)
package base62

import (
	"errors"
	"fmt"
	"math/big"
)

const (
	// MaxInputBytes bounds big.Int allocation for untrusted binary input.
	MaxInputBytes = 1 << 20
	// MaxEncodedBytes is a conservative bound for MaxInputBytes in base62.
	MaxEncodedBytes = 1_500_000
)

var (
	ErrInputTooLarge   = errors.New("base62: input too large")
	ErrInvalidEncoding = errors.New("base62: invalid encoding")
)

// Alphabet is the base62 character set, ordered by ASCII value so encoded
// strings of equal length sort like the bytes they encode.
const Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var (
	base = big.NewInt(int64(len(Alphabet)))

	// decodeMap maps an ASCII byte to its alphabet index, -1 for invalid.
	decodeMap [128]int8
)

func init() {
	for i := range decodeMap {
		decodeMap[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeMap[Alphabet[i]] = int8(i)
	}
}

// Encode encodes b as base62. Leading zero bytes are preserved as leading
// '0' characters (the base58 convention), so Decode(Encode(b)) always
// returns exactly b.
func Encode(b []byte) (string, error) {
	if len(b) > MaxInputBytes {
		return "", ErrInputTooLarge
	}
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}

	n := new(big.Int).SetBytes(b[zeros:])
	mod := new(big.Int)
	capacity := zeros + (len(b)*3)/2 + 1
	if capacity > MaxEncodedBytes {
		capacity = MaxEncodedBytes
	}
	out := make([]byte, 0, capacity)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, Alphabet[0])
	}

	// Digits were produced least-significant first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > MaxEncodedBytes {
		return "", ErrInputTooLarge
	}
	return string(out), nil
}

// Decode decodes a base62 string produced by Encode. It fails on any
// character outside the alphabet.
func Decode(s string) ([]byte, error) {
	if len(s) > MaxEncodedBytes {
		return nil, ErrInputTooLarge
	}
	zeros := 0
	for zeros < len(s) && s[zeros] == Alphabet[0] {
		zeros++
	}

	n := new(big.Int)
	for i := zeros; i < len(s); i++ {
		c := s[i]
		if c >= 128 || decodeMap[c] < 0 {
			return nil, fmt.Errorf(
				"%w: invalid character %q at position %d",
				ErrInvalidEncoding,
				c,
				i,
			)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(decodeMap[c])))
	}

	digits := n.Bytes()
	if zeros+len(digits) > MaxInputBytes {
		return nil, ErrInputTooLarge
	}
	out := make([]byte, zeros+len(digits))
	copy(out[zeros:], digits)
	return out, nil
}
