package stripe

import (
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	if _, appErr := New(payment.Config{}); appErr == nil {
		t.Fatal("New must reject an empty API key")
	}

	if _, appErr := New(payment.Config{APIKey: "sk_test"}); appErr != nil {
		t.Fatalf("New with an API key failed: %v", appErr)
	}
}

func TestDriverSelfRegistersAndOpens(t *testing.T) {
	t.Parallel()

	found := false
	for _, name := range payment.Providers() {
		if name == ProviderName {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("driver did not self-register; providers = %v", payment.Providers())
	}

	p, appErr := payment.Open(ProviderName, payment.Config{APIKey: "sk_test"})
	if appErr != nil {
		t.Fatalf("Open(stripe) failed: %v", appErr)
	}

	if p.Name() != ProviderName {
		t.Fatalf("Name() = %q", p.Name())
	}

	// A fully-featured provider answers every capability query.
	if _, ok := payment.Refunds(p); !ok {
		t.Error("stripe must support refunds")
	}

	if _, ok := payment.Events(p); !ok {
		t.Error("stripe must support event decoding")
	}

	if _, ok := payment.Subscriptions(p); !ok {
		t.Error("stripe must support subscriptions")
	}
}
