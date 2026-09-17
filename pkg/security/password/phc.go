package password

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const argon2Version = 19

type phcHash struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

func formatPHC(hash phcHash) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		hash.memoryKiB,
		hash.iterations,
		hash.parallelism,
		base64.RawStdEncoding.EncodeToString(hash.salt),
		base64.RawStdEncoding.EncodeToString(hash.key),
	)
}

func parsePHC(encoded string, policy Policy) (phcHash, error) {
	if len(encoded) > policy.MaxPHCBytes {
		return phcHash{}, fmt.Errorf("%w: encoded hash is too long", ErrHashPolicy)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return phcHash{}, fmt.Errorf("%w: invalid Argon2id PHC structure", ErrMalformedHash)
	}
	if parts[2] != "v=19" {
		return phcHash{}, fmt.Errorf("%w: unsupported Argon2 version", ErrMalformedHash)
	}

	parameters, err := parseParameters(parts[3])
	if err != nil {
		return phcHash{}, err
	}
	if parameters.memoryKiB > policy.MemoryKiB ||
		parameters.iterations > policy.Iterations ||
		parameters.parallelism > policy.Parallelism {
		return phcHash{}, fmt.Errorf("%w: Argon2 work factor exceeds configured bound", ErrHashPolicy)
	}
	if parameters.memoryKiB < uint32(8)*uint32(parameters.parallelism) {
		return phcHash{}, fmt.Errorf("%w: Argon2 memory is below the safe lane minimum", ErrMalformedHash)
	}

	salt, err := decodeBounded(parts[4], 8, int(policy.SaltBytes), "salt")
	if err != nil {
		return phcHash{}, err
	}
	key, err := decodeBounded(parts[5], 16, int(policy.KeyBytes), "key")
	if err != nil {
		wipe(salt)
		return phcHash{}, err
	}
	return phcHash{
		memoryKiB:   parameters.memoryKiB,
		iterations:  parameters.iterations,
		parallelism: parameters.parallelism,
		salt:        salt,
		key:         key,
	}, nil
}

type phcParameters struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

func parseParameters(raw string) (phcParameters, error) {
	values := make(map[string]uint64, 3)
	fields := strings.Split(raw, ",")
	if len(fields) != 3 {
		return phcParameters{}, fmt.Errorf("%w: expected exactly m,t,p", ErrMalformedHash)
	}
	for _, field := range fields {
		key, value, found := strings.Cut(field, "=")
		if !found || (key != "m" && key != "t" && key != "p") {
			return phcParameters{}, fmt.Errorf("%w: unknown Argon2 parameter", ErrMalformedHash)
		}
		if _, duplicate := values[key]; duplicate {
			return phcParameters{}, fmt.Errorf("%w: duplicate Argon2 parameter", ErrMalformedHash)
		}
		if value == "" || strings.IndexFunc(value, func(r rune) bool {
			return r < '0' || r > '9'
		}) >= 0 {
			return phcParameters{}, fmt.Errorf("%w: invalid Argon2 parameter value", ErrMalformedHash)
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil || parsed == 0 {
			return phcParameters{}, fmt.Errorf("%w: invalid Argon2 parameter value", ErrMalformedHash)
		}
		values[key] = parsed
	}
	if values["m"] > uint64(^uint32(0)) ||
		values["t"] > uint64(^uint32(0)) ||
		values["p"] > uint64(^uint8(0)) {
		return phcParameters{}, fmt.Errorf("%w: Argon2 parameter overflows", ErrHashPolicy)
	}
	return phcParameters{
		memoryKiB:   uint32(values["m"]),
		iterations:  uint32(values["t"]),
		parallelism: uint8(values["p"]),
	}, nil
}

func decodeBounded(raw string, minimum, maximum int, label string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty Argon2 %s", ErrMalformedHash, label)
	}
	decodedLength := base64.RawStdEncoding.DecodedLen(len(raw))
	if decodedLength > maximum {
		return nil, fmt.Errorf("%w: Argon2 %s exceeds configured bound", ErrHashPolicy, label)
	}
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Argon2 %s encoding", ErrMalformedHash, label)
	}
	if len(decoded) < minimum {
		return nil, fmt.Errorf("%w: Argon2 %s is too short", ErrMalformedHash, label)
	}
	return decoded, nil
}

func (h phcHash) needsRehash(policy Policy) bool {
	return h.memoryKiB != policy.MemoryKiB ||
		h.iterations != policy.Iterations ||
		h.parallelism != policy.Parallelism ||
		len(h.salt) != int(policy.SaltBytes) ||
		len(h.key) != int(policy.KeyBytes)
}
