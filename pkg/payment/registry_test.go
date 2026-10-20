package payment

import (
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

// fakeProvider is a minimal provider used to exercise the registry without a
// real SDK.
type fakeProvider struct{ name string }

func (f fakeProvider) Name() string { return f.name }

func TestRegisterAndOpen(t *testing.T) {
	resetRegistry(t)

	Register("fake", func(cfg Config) (Provider, *apperr.AppError) {
		return fakeProvider{name: "fake"}, nil
	})

	p, appErr := Open("fake", Config{})
	if appErr != nil {
		t.Fatalf("Open returned error: %v", appErr)
	}

	if p.Name() != "fake" {
		t.Fatalf("Name() = %q", p.Name())
	}
}

func TestOpenUnknownProvider(t *testing.T) {
	resetRegistry(t)

	_, appErr := Open("nope", Config{})
	if appErr == nil {
		t.Fatal("Open of an unknown provider must fail")
	}

	if appErr.Code != CodePaymentInvalid {
		t.Fatalf("code = %d, want %d", appErr.Code, CodePaymentInvalid)
	}
}

func TestProvidersIsSorted(t *testing.T) {
	resetRegistry(t)

	factory := func(cfg Config) (Provider, *apperr.AppError) { return fakeProvider{}, nil }
	Register("stripe", factory)
	Register("adyen", factory)
	Register("momo", factory)

	got := Providers()
	want := []string{"adyen", "momo", "stripe"}

	if len(got) != len(want) {
		t.Fatalf("Providers() = %v", got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Providers() = %v, want %v", got, want)
		}
	}
}

func TestRegisterRejectsEmptyNameNilFactoryAndDuplicate(t *testing.T) {
	resetRegistry(t)

	assertPanics(t, "empty name", func() { Register("", func(Config) (Provider, *apperr.AppError) { return nil, nil }) })
	assertPanics(t, "nil factory", func() { Register("x", nil) })

	Register("dup", func(Config) (Provider, *apperr.AppError) { return fakeProvider{}, nil })
	assertPanics(t, "duplicate", func() {
		Register("dup", func(Config) (Provider, *apperr.AppError) { return fakeProvider{}, nil })
	})
}

func TestConfigOption(t *testing.T) {
	t.Parallel()

	cfg := Config{Options: map[string]string{"vnp_url": "https://sandbox"}}

	if v, ok := cfg.Option("vnp_url"); !ok || v != "https://sandbox" {
		t.Fatalf("Option = %q, %t", v, ok)
	}

	if _, ok := (Config{}).Option("missing"); ok {
		t.Fatal("missing option must report ok=false")
	}
}

// resetRegistry swaps in a clean registry for the test and restores the real one
// afterwards, so registry tests do not see each other's or the drivers' state.
func resetRegistry(t *testing.T) {
	t.Helper()

	registryMu.Lock()
	saved := registry
	registry = map[string]Factory{}
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic", name)
		}
	}()

	fn()
}
