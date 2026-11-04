// Package password provides bounded Argon2id hashing and legacy bcrypt
// verification. A Hasher owns a fixed concurrency budget; callers must not copy
// it after construction.
package password

import (
	"crypto/rand"
	"io"
	"sync"

	"github.com/huynhanx03/go-common/pkg/settings"
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

func New(config settings.Password, optionValues ...Option) (*Hasher, error) {
	policy := DefaultPolicy()
	policy.MemoryKiB = config.MemoryKiB
	policy.Iterations = config.Iterations
	policy.Parallelism = config.Parallelism
	policy.MaxConcurrent = config.MaxConcurrent
	return NewWithPolicy(policy, optionValues...)
}

func NewWithPolicy(policy Policy, optionValues ...Option) (*Hasher, error) {
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
