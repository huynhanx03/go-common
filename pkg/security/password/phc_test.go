package password

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestPHCRoundTripAndRehashDecision(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()
	hash := phcHash{
		memoryKiB:   policy.MemoryKiB,
		iterations:  policy.Iterations,
		parallelism: policy.Parallelism,
		salt:        []byte("1234567890123456"),
		key:         []byte("12345678901234567890123456789012"),
	}
	encoded := formatPHC(hash)
	parsed, err := parsePHC(encoded, policy)
	if err != nil {
		t.Fatalf("parsePHC() error = %v", err)
	}
	if parsed.memoryKiB != hash.memoryKiB || parsed.iterations != hash.iterations ||
		parsed.parallelism != hash.parallelism || string(parsed.salt) != string(hash.salt) ||
		string(parsed.key) != string(hash.key) {
		t.Fatalf("parsePHC() = %+v, want %+v", parsed, hash)
	}
	if parsed.needsRehash(policy) {
		t.Fatal("needsRehash() = true for matching policy")
	}

	changes := []struct {
		name   string
		change func(*phcHash)
	}{
		{name: "memory", change: func(value *phcHash) { value.memoryKiB-- }},
		{name: "iterations", change: func(value *phcHash) { value.iterations-- }},
		{name: "parallelism", change: func(value *phcHash) { value.parallelism-- }},
		{name: "salt length", change: func(value *phcHash) { value.salt = value.salt[:len(value.salt)-1] }},
		{name: "key length", change: func(value *phcHash) { value.key = value.key[:len(value.key)-1] }},
	}
	for _, change := range changes {
		change := change
		t.Run(change.name, func(t *testing.T) {
			t.Parallel()
			modified := parsed
			modified.salt = append([]byte(nil), parsed.salt...)
			modified.key = append([]byte(nil), parsed.key...)
			change.change(&modified)
			if !modified.needsRehash(policy) {
				t.Fatal("needsRehash() = false after policy mismatch")
			}
		})
	}
}

func TestParsePHCRejectsMalformedAndOutOfPolicyValues(t *testing.T) {
	t.Parallel()

	policy := testPolicy(32, 1, 1)
	salt := []byte("12345678")
	key := []byte("1234567890123456")
	valid := formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: salt, key: key})
	tooMuchWork := formatPHC(phcHash{memoryKiB: 33, iterations: 1, parallelism: 1, salt: salt, key: key})
	tooLittleMemory := formatPHC(phcHash{memoryKiB: 7, iterations: 1, parallelism: 1, salt: salt, key: key})
	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{name: "too long", encoded: strings.Repeat("x", policy.MaxPHCBytes+1), want: ErrHashPolicy},
		{name: "bad structure", encoded: "not-a-phc-hash", want: ErrMalformedHash},
		{name: "wrong algorithm", encoded: strings.Replace(valid, "$argon2id$", "$argon2i$", 1), want: ErrMalformedHash},
		{name: "unsupported version", encoded: strings.Replace(valid, "$v=19$", "$v=18$", 1), want: ErrMalformedHash},
		{name: "bad parameters", encoded: strings.Replace(valid, "m=32,t=1,p=1", "m=,t=1,p=1", 1), want: ErrMalformedHash},
		{name: "work exceeds policy", encoded: tooMuchWork, want: ErrHashPolicy},
		{name: "memory below lane minimum", encoded: tooLittleMemory, want: ErrMalformedHash},
		{name: "empty salt", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, key: key}), want: ErrMalformedHash},
		{name: "salt exceeds policy", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: []byte(strings.Repeat("s", 9)), key: key}), want: ErrHashPolicy},
		{name: "invalid salt encoding", encoded: strings.Replace(valid, base64.RawStdEncoding.EncodeToString(salt), "!!!!!!!!!!!", 1), want: ErrMalformedHash},
		{name: "salt too short", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: []byte("1234"), key: key}), want: ErrMalformedHash},
		{name: "empty key", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: salt}), want: ErrMalformedHash},
		{name: "key exceeds policy", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: salt, key: []byte(strings.Repeat("k", 17))}), want: ErrHashPolicy},
		{name: "invalid key encoding", encoded: strings.Replace(valid, base64.RawStdEncoding.EncodeToString(key), "!!!!!!!!!!!!!!!!!!!!!!", 1), want: ErrMalformedHash},
		{name: "key too short", encoded: formatPHC(phcHash{memoryKiB: 32, iterations: 1, parallelism: 1, salt: salt, key: []byte("1234")}), want: ErrMalformedHash},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parsePHC(test.encoded, policy); !errors.Is(err, test.want) {
				t.Fatalf("parsePHC() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseParametersAcceptsOrderAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	parsed, err := parseParameters("p=2,m=64,t=3")
	if err != nil {
		t.Fatalf("parseParameters() error = %v", err)
	}
	if parsed != (phcParameters{memoryKiB: 64, iterations: 3, parallelism: 2}) {
		t.Fatalf("parseParameters() = %+v", parsed)
	}

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{name: "wrong field count", raw: "m=1,t=1", want: ErrMalformedHash},
		{name: "missing separator", raw: "m1,t=1,p=1", want: ErrMalformedHash},
		{name: "unknown field", raw: "m=1,t=1,x=1", want: ErrMalformedHash},
		{name: "duplicate field", raw: "m=1,m=2,p=1", want: ErrMalformedHash},
		{name: "empty value", raw: "m=,t=1,p=1", want: ErrMalformedHash},
		{name: "nondigit value", raw: "m=1x,t=1,p=1", want: ErrMalformedHash},
		{name: "uint32 overflow", raw: "m=4294967296,t=1,p=1", want: ErrMalformedHash},
		{name: "zero value", raw: "m=1,t=0,p=1", want: ErrMalformedHash},
		{name: "parallelism overflow", raw: "m=2048,t=1,p=256", want: ErrHashPolicy},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseParameters(test.raw); !errors.Is(err, test.want) {
				t.Fatalf("parseParameters() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeBoundedEnforcesEncodingAndLengths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		min  int
		max  int
		want error
	}{
		{name: "valid", raw: base64.RawStdEncoding.EncodeToString([]byte("12345678")), min: 8, max: 8},
		{name: "empty", min: 8, max: 16, want: ErrMalformedHash},
		{name: "too large", raw: base64.RawStdEncoding.EncodeToString([]byte("123456789")), min: 8, max: 8, want: ErrHashPolicy},
		{name: "invalid base64", raw: "!!!!!!!!!!!!", min: 8, max: 16, want: ErrMalformedHash},
		{name: "too short", raw: base64.RawStdEncoding.EncodeToString([]byte("1234")), min: 8, max: 16, want: ErrMalformedHash},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := decodeBounded(test.raw, test.min, test.max, "salt")
			if !errors.Is(err, test.want) {
				t.Fatalf("decodeBounded() error = %v, want %v", err, test.want)
			}
			if test.want == nil && len(value) != test.min {
				t.Fatalf("decoded length = %d, want %d", len(value), test.min)
			}
		})
	}
}

func testPolicy(memory uint32, iterations uint32, parallelism uint8) Policy {
	policy := DefaultPolicy()
	policy.MemoryKiB = memory
	policy.Iterations = iterations
	policy.Parallelism = parallelism
	policy.SaltBytes = 8
	policy.KeyBytes = 16
	return policy
}
