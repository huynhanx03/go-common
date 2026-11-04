package password

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

func (h *Hasher) Verify(
	ctx context.Context,
	encoded string,
	password []byte,
) (Verification, error) {
	if err := h.validateInput(ctx, password); err != nil {
		return Verification{}, err
	}
	if len(encoded) > h.policy.MaxPHCBytes {
		return Verification{}, fmt.Errorf("%w: encoded hash is too long", ErrHashPolicy)
	}

	switch {
	case strings.HasPrefix(encoded, argon2idPHCPrefix):
		return h.verifyArgon2id(ctx, encoded, password)
	case isBcrypt(encoded):
		return h.verifyBcrypt(ctx, encoded, password)
	default:
		return Verification{}, fmt.Errorf("%w: algorithm is not accepted", ErrUnsupportedHash)
	}
}

func (h *Hasher) verifyArgon2id(
	ctx context.Context,
	encoded string,
	password []byte,
) (Verification, error) {
	parsed, err := parsePHC(encoded, h.policy)
	if err != nil {
		return Verification{}, err
	}
	defer wipe(parsed.salt)
	defer wipe(parsed.key)
	if err := h.acquire(ctx); err != nil {
		return Verification{}, err
	}
	defer h.release()

	actual := argon2.IDKey(
		password,
		parsed.salt,
		parsed.iterations,
		parsed.memoryKiB,
		parsed.parallelism,
		uint32(len(parsed.key)),
	)
	defer wipe(actual)
	valid := subtle.ConstantTimeCompare(actual, parsed.key) == 1
	return Verification{
		Valid:       valid,
		NeedsRehash: valid && parsed.needsRehash(h.policy),
		Algorithm:   argon2idAlgorithm,
	}, nil
}

func (h *Hasher) verifyBcrypt(
	ctx context.Context,
	encoded string,
	password []byte,
) (Verification, error) {
	cost, err := bcrypt.Cost([]byte(encoded))
	if err != nil {
		return Verification{}, fmt.Errorf("%w: invalid bcrypt encoding", ErrMalformedHash)
	}
	if cost < minBcryptCost || cost > maxBcryptCost {
		return Verification{}, fmt.Errorf("%w: bcrypt cost %d is outside %d..%d", ErrHashPolicy, cost, minBcryptCost, maxBcryptCost)
	}
	if len(password) > maxBcryptPasswordBytes {
		return Verification{}, fmt.Errorf("%w: bcrypt password exceeds %d bytes", ErrHashPolicy, maxBcryptPasswordBytes)
	}
	if err := h.acquire(ctx); err != nil {
		return Verification{}, err
	}
	defer h.release()

	err = bcrypt.CompareHashAndPassword([]byte(encoded), password)
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return Verification{Algorithm: bcryptAlgorithm}, nil
	}
	if err != nil {
		return Verification{}, fmt.Errorf("%w: invalid bcrypt encoding", ErrMalformedHash)
	}
	return Verification{
		Valid:       true,
		NeedsRehash: true,
		Algorithm:   bcryptAlgorithm,
	}, nil
}

func isBcrypt(encoded string) bool {
	return strings.HasPrefix(encoded, "$2a$") ||
		strings.HasPrefix(encoded, "$2b$") ||
		strings.HasPrefix(encoded, "$2y$")
}
