package password

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/huynhanx03/go-common/pkg/settings"
)

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type doneContext struct {
	context.Context
	errCalls int
}

func (*doneContext) Done() <-chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}

func (ctx *doneContext) Err() error {
	ctx.errCalls++
	if ctx.errCalls == 1 {
		return nil
	}
	return context.Canceled
}

func TestHashRejectsInvalidInputsAndUnavailableHasher(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, DefaultPassword())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name     string
		hasher   *Hasher
		ctx      context.Context
		password []byte
		want     error
	}{
		{name: "nil hasher", ctx: context.Background(), password: []byte("secret"), want: ErrInvalidPolicy},
		{name: "nil context", hasher: hasher, password: []byte("secret"), want: ErrInvalidContext},
		{name: "canceled context", hasher: hasher, ctx: ctx, password: []byte("secret"), want: context.Canceled},
		{name: "empty password", hasher: hasher, ctx: context.Background(), want: ErrEmptyPassword},
		{name: "password exceeds configured size", hasher: newTestHasher(t, testPasswordSettings(32, 1, 1, 1)), ctx: context.Background(), password: make([]byte, defaultMaxPasswordBytes+1), want: ErrPasswordTooLong},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.hasher.Hash(test.ctx, test.password)
			if !errors.Is(err, test.want) {
				t.Fatalf("Hash() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestHashHandlesRandomAndConcurrencyFailures(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, DefaultPassword(), WithRandom(errorReader{}))
	if _, err := hasher.Hash(context.Background(), []byte("secret")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Hash() random error = %v, want wrapped reader error", err)
	}

	busy := newTestHasher(t, testPasswordSettings(32, 1, 1, 1))
	busy.slots <- struct{}{}
	if _, err := busy.Hash(&doneContext{Context: context.Background()}, []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash() while full = %v, want context.Canceled", err)
	}
}

func TestHashCreatesVerifiablePHC(t *testing.T) {
	t.Parallel()

	hasher := newTestHasher(t, testPasswordSettings(32, 1, 1, 1))
	encoded, err := hasher.Hash(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if _, err := parsePHC(encoded, hasher.policy); err != nil {
		t.Fatalf("Hash() produced invalid PHC: %v", err)
	}
}

func newTestHasher(t *testing.T, config settings.Password, options ...Option) *Hasher {
	t.Helper()
	hasher, err := New(config, options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return hasher
}

func testPasswordSettings(memory uint32, iterations uint32, parallelism uint8, concurrent int) settings.Password {
	return settings.Password{
		MemoryKiB:     memory,
		Iterations:    iterations,
		Parallelism:   parallelism,
		MaxConcurrent: concurrent,
	}
}
