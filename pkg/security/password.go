package security

import (
	"context"
	"sync"

	focused "github.com/huynhanx03/go-common/pkg/security/password"
	"golang.org/x/crypto/bcrypt"
)

const (
	DefaultCost = 12
)

var (
	defaultHasherOnce sync.Once
	defaultHasher     *focused.Hasher
	defaultHasherErr  error
)

// HashPassword delegates to the bounded Argon2id default hasher.
//
// Deprecated: use password.New and (*password.Hasher).Hash.
func HashPassword(password string) (string, error) {
	hasher, err := compatibilityHasher()
	if err != nil {
		return "", err
	}
	return hasher.Hash(context.Background(), []byte(password))
}

// ComparePassword verifies Argon2id and bounded legacy bcrypt hashes.
//
// Deprecated: use (*password.Hasher).Verify.
func ComparePassword(hashedPassword, password string) error {
	hasher, err := compatibilityHasher()
	if err != nil {
		return err
	}
	verification, err := hasher.Verify(
		context.Background(),
		hashedPassword,
		[]byte(password),
	)
	if err != nil {
		return err
	}
	if !verification.Valid {
		return bcrypt.ErrMismatchedHashAndPassword
	}
	return nil
}

func compatibilityHasher() (*focused.Hasher, error) {
	defaultHasherOnce.Do(func() {
		defaultHasher, defaultHasherErr = focused.New(focused.DefaultPolicy())
	})
	return defaultHasher, defaultHasherErr
}
