package payment

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

// refundOnlyProvider supports exactly one capability, proving a partial provider
// is valid and that capability discovery reports only what it implements.
type refundOnlyProvider struct{ fakeProvider }

func (refundOnlyProvider) RefundInFull(context.Context, *FullRefund) (*Refund, *apperr.AppError) {
	return &Refund{}, nil
}

func (refundOnlyProvider) RefundAmount(context.Context, *PartialRefund) (*Refund, *apperr.AppError) {
	return &Refund{}, nil
}

func TestCapabilityDiscovery(t *testing.T) {
	t.Parallel()

	var p Provider = refundOnlyProvider{fakeProvider{name: "partial"}}

	if _, ok := Refunds(p); !ok {
		t.Error("Refunds must be supported")
	}

	if _, ok := Subscriptions(p); ok {
		t.Error("Subscriptions must not be supported by a refund-only provider")
	}

	if _, ok := Invoices(p); ok {
		t.Error("Invoices must not be supported by a refund-only provider")
	}
}

func TestCapabilityDiscoveryBareProvider(t *testing.T) {
	t.Parallel()

	var p Provider = fakeProvider{name: "bare"}

	if _, ok := Refunds(p); ok {
		t.Error("a bare provider supports no capabilities")
	}

	if _, ok := Events(p); ok {
		t.Error("a bare provider supports no capabilities")
	}
}
