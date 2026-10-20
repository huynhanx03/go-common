package payment

import "time"

// SubscriptionStatus mirrors the lifecycle a recurring subscription moves
// through. Providers that report a status outside this set have it preserved
// verbatim (see the doc.go contract).
type SubscriptionStatus string

const (
	SubscriptionStatusActive            SubscriptionStatus = "active"
	SubscriptionStatusTrialing          SubscriptionStatus = "trialing"
	SubscriptionStatusPastDue           SubscriptionStatus = "past_due"
	SubscriptionStatusCanceled          SubscriptionStatus = "canceled"
	SubscriptionStatusIncomplete        SubscriptionStatus = "incomplete"
	SubscriptionStatusIncompleteExpired SubscriptionStatus = "incomplete_expired"
	SubscriptionStatusUnpaid            SubscriptionStatus = "unpaid"
	SubscriptionStatusPaused            SubscriptionStatus = "paused"
)

// Subscription is the neutral projection of a recurring subscription.
type Subscription struct {
	Ref                string
	CustomerRef        string
	CustomerAccountRef string

	ScheduleRef string

	DefaultPaymentMethodRef string
	LatestInvoiceRef        string
	Status                  SubscriptionStatus
	CancelAtPeriodEnd       bool
	LatestInvoicePaid       bool
	Items                   []SubscriptionItem
	Metadata                map[string]string

	LatestInvoice *Invoice

	PlanPriceRef   string
	PlanProductRef string
}

// SubscriptionItem is one priced line of a subscription.
type SubscriptionItem struct {
	Ref                string
	PriceRef           string
	ProductRef         string
	UnitAmount         Money
	CurrentPeriodStart *time.Time
	CurrentPeriodEnd   *time.Time
}

// FirstItem returns the subscription's first item, or false when it has none.
func (s *Subscription) FirstItem() (SubscriptionItem, bool) {
	if s == nil || len(s.Items) == 0 {
		return SubscriptionItem{}, false
	}

	return s.Items[0], true
}

// ProrationMode selects how a provider settles the difference when a plan
// changes mid-cycle.
type ProrationMode int

const (
	ProrationNone ProrationMode = iota + 1
	ProrationAlwaysInvoice
)

// String returns the provider-neutral wire token for the mode, or "" when the
// mode is not a defined value.
func (m ProrationMode) String() string {
	switch m {
	case ProrationNone:
		return "none"
	case ProrationAlwaysInvoice:
		return "always_invoice"
	default:
		return ""
	}
}

// ParseProrationMode is the inverse of String; ok is false for unknown input.
func ParseProrationMode(v string) (ProrationMode, bool) {
	switch v {
	case "none":
		return ProrationNone, true
	case "always_invoice":
		return ProrationAlwaysInvoice, true
	default:
		return 0, false
	}
}

// PlanItemChange re-prices one subscription item.
type PlanItemChange struct {
	SubscriptionRef string
	ItemRef         string
	PriceRef        string
	Proration       ProrationMode

	ProrationDate *time.Time

	DeferPayment   bool
	IdempotencyKey string
}

// DefaultPaymentMethodChange sets the payment method a subscription bills.
type DefaultPaymentMethodChange struct {
	SubscriptionRef  string
	PaymentMethodRef string
	IdempotencyKey   string
}

// SubscriptionRef names a subscription for an idempotent mutation.
type SubscriptionRef struct {
	SubscriptionRef string
	IdempotencyKey  string
}
