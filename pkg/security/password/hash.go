package password

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"golang.org/x/crypto/argon2"
)

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
		return "", fmt.Errorf("read random salt: %w", err)
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
	return formatPHC(phcHash{
		memoryKiB:   h.policy.MemoryKiB,
		iterations:  h.policy.Iterations,
		parallelism: h.policy.Parallelism,
		salt:        salt,
		key:         key,
	}), nil
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

// wipe removes short-lived secret-derived bytes on a best-effort basis before
// their backing storage becomes unreachable.
func wipe(value []byte) {
	clear(value)
	runtime.KeepAlive(value)
}
