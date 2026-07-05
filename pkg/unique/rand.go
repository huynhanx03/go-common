package unique

import (
	"crypto/rand"

	"github.com/huynhanx03/go-common/pkg/encoding/base62"
)

// RandBase62 returns n random characters from the base62 alphabet.
// Randomness comes from crypto/rand — which cannot fail since Go 1.24 (the
// runtime aborts instead) — with rejection sampling so every character is
// uniform (no modulo bias).
func RandBase62(n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		chunk := make([]byte, n-len(out))
		rand.Read(chunk)
		for _, b := range chunk {
			// Reject bytes ≥ 248 (= 4×62) so b%62 stays uniform.
			if b >= 248 {
				continue
			}
			out = append(out, base62.Alphabet[int(b)%len(base62.Alphabet)])
		}
	}
	return string(out)
}
