// Package password provides bounded Argon2id hashing and legacy bcrypt
// verification. A Hasher owns a fixed concurrency budget; callers must not copy
// it after construction.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	minBcryptCost = 10
	maxBcryptCost = 14
)

var (
	ErrInvalidPolicy   = errors.New("password: invalid policy")
	ErrEmptyPassword   = errors.New("password: empty password")
	ErrPasswordTooLong = errors.New("password: password too long")
	ErrMalformedHash   = errors.New("password: malformed hash")
	ErrUnsupportedHash = errors.New("password: unsupported hash")
	ErrHashPolicy      = errors.New("password: hash exceeds verification policy")
	ErrInvalidContext  = errors.New("password: nil context")
)

type Verification struct {
	Valid       bool
	NeedsRehash bool
	Algorithm   string
}

type Option func(*options) error

type options struct {
	random io.Reader
}

func WithRandom(reader io.Reader) Option {
	return func(options *options) error {
		if reader == nil {
			return invalidPolicy("random source is nil")
		}
		options.random = reader
		return nil
	}
}

type Hasher struct {
	policy Policy
	random io.Reader
	slots  chan struct{}
	randMu sync.Mutex
}

func New(policy Policy, optionValues ...Option) (*Hasher, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	configuration := options{random: rand.Reader}
	for _, option := range optionValues {
		if option == nil {
			return nil, invalidPolicy("nil option")
		}
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	return &Hasher{
		policy: policy,
		random: configuration.random,
		slots:  make(chan struct{}, policy.MaxConcurrent),
	}, nil
}

func (h *Hasher) Hash(ctx context.Context, password []byte) (string, error) {
	if err := h.validateInput(ctx, password); err != nil {
		return "", err
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	salt := make([]byte, h.policy.SaltBytes)
	defer wipe(salt)
	h.randMu.Lock()
	_, err := io.ReadFull(h.random, salt)
	h.randMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("password: read random salt: %w", err)
	}

	key := argon2.IDKey(
		password,
		salt,
		h.policy.Iterations,
		h.policy.MemoryKiB,
		h.policy.Parallelism,
		h.policy.KeyBytes,
	)
	defer wipe(key)
	encoded := formatPHC(phcHash{
		memoryKiB:   h.policy.MemoryKiB,
		iterations:  h.policy.Iterations,
		parallelism: h.policy.Parallelism,
		salt:        salt,
		key:         key,
	})
	return encoded, nil
}

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
	case strings.HasPrefix(encoded, "$argon2id$"):
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
		Algorithm:   "argon2id",
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
	if len(password) > 72 {
		return Verification{}, fmt.Errorf("%w: bcrypt password exceeds 72 bytes", ErrHashPolicy)
	}
	if err := h.acquire(ctx); err != nil {
		return Verification{}, err
	}
	defer h.release()

	err = bcrypt.CompareHashAndPassword([]byte(encoded), password)
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return Verification{Algorithm: "bcrypt"}, nil
	}
	if err != nil {
		return Verification{}, fmt.Errorf("%w: invalid bcrypt encoding", ErrMalformedHash)
	}
	return Verification{
		Valid:       true,
		NeedsRehash: true,
		Algorithm:   "bcrypt",
	}, nil
}

func (h *Hasher) validateInput(ctx context.Context, password []byte) error {
	if h == nil || h.random == nil || h.slots == nil {
		return ErrInvalidPolicy
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(password) == 0 {
		return ErrEmptyPassword
	}
	if len(password) > h.policy.MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() {
	<-h.slots
}

func isBcrypt(encoded string) bool {
	return strings.HasPrefix(encoded, "$2a$") ||
		strings.HasPrefix(encoded, "$2b$") ||
		strings.HasPrefix(encoded, "$2y$")
}

// wipe removes short-lived secret-derived bytes on a best-effort basis before
// their backing storage becomes unreachable.
func wipe(value []byte) {
	clear(value)
	runtime.KeepAlive(value)
}
