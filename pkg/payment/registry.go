package payment

import (
	"sort"
	"sync"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/logger"
)

// Config carries the settings a provider needs to build itself. Common fields
// are named; provider-specific extras go in Options (e.g. a regional gateway's
// endpoint). Logger is go-common's logger, injected so drivers never construct
// their own.
type Config struct {
	APIKey        string
	WebhookSecret string
	Options       map[string]string
	Logger        *logger.LoggerZap
}

// Option returns an Options value and whether it was set.
func (c Config) Option(key string) (string, bool) {
	if c.Options == nil {
		return "", false
	}

	v, ok := c.Options[key]
	return v, ok
}

// Factory builds a provider from a Config. Drivers register one in init.
type Factory func(cfg Config) (Provider, *apperr.AppError)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a provider available to Open under name. Drivers call it from
// init, so a blank import wires the provider in. It panics on an empty name, a
// nil factory, or a duplicate name — all of which are start-up programming
// errors, not runtime conditions.
func Register(name string, factory Factory) {
	if name == "" {
		panic("payment: Register called with an empty provider name")
	}

	if factory == nil {
		panic("payment: Register called with a nil factory for provider " + name)
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[name]; exists {
		panic("payment: provider already registered: " + name)
	}

	registry[name] = factory
}

// Open builds the provider registered under name. It returns an *apperr.AppError
// when no provider is registered under that name.
func Open(name string, cfg Config) (Provider, *apperr.AppError) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()

	if !ok {
		return nil, apperr.New(CodePaymentInvalid, "payment: unknown provider "+name, nil)
	}

	return factory(cfg)
}

// Providers lists the registered provider names in sorted order.
func Providers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
