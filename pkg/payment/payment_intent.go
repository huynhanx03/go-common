package payment

import "time"

// PaymentIntentStatus is the neutral status of a one-off payment attempt.
type PaymentIntentStatus string

const (
	PaymentIntentStatusRequiresPaymentMethod PaymentIntentStatus = "requires_payment_method"
	PaymentIntentStatusRequiresConfirmation  PaymentIntentStatus = "requires_confirmation"
	PaymentIntentStatusRequiresAction        PaymentIntentStatus = "requires_action"
	PaymentIntentStatusProcessing            PaymentIntentStatus = "processing"
	PaymentIntentStatusRequiresCapture       PaymentIntentStatus = "requires_capture"
	PaymentIntentStatusCanceled              PaymentIntentStatus = "canceled"
	PaymentIntentStatusSucceeded             PaymentIntentStatus = "succeeded"
)

// PaymentError is the neutral form of a provider's payment failure detail,
// carried on the object that failed (an invoice or a payment intent).
type PaymentError struct {
	Code        string
	DeclineCode string
	Message     string
}

// PaymentIntent is the neutral projection of a one-off payment attempt.
type PaymentIntent struct {
	Ref                string
	CustomerRef        string
	CustomerAccountRef string
	PaymentMethodRef   string
	Status             PaymentIntentStatus
	Amount             Money
	ClientSecret       string
	Created            *time.Time
	Metadata           map[string]string
	LastPaymentError   *PaymentError
	CardBrand          string
	CardLast4          string
}

// Confirmed reports whether the intent has money committed or captured, so a
// caller can treat the payment as good even before final settlement.
func (p *PaymentIntent) Confirmed() bool {
	if p == nil {
		return false
	}

	switch p.Status {
	case PaymentIntentStatusProcessing,
		PaymentIntentStatusSucceeded,
		PaymentIntentStatusRequiresCapture:
		return true
	default:
		return false
	}
}

// PaymentIntentRef names a payment intent for an idempotent mutation.
type PaymentIntentRef struct {
	PaymentIntentRef string
	IdempotencyKey   string
}

// OneTimeCharge creates an immediate, non-recurring charge. It is a low-level
// primitive without tax; for a taxed one-off payment use OneTimeCheckout, whose
// hosted flow can collect the location automatic tax needs.
//
// When ManualCapture is set the charge only authorizes the funds; call
// CapturePayment later to settle them (an auth-then-capture flow). Otherwise the
// charge captures immediately.
type OneTimeCharge struct {
	Amount             Money
	Description        string
	CustomerAccountRef string
	ManualCapture      bool
	Metadata           map[string]string
	IdempotencyKey     string
}

// PaymentCapture settles the funds of a previously authorized payment intent.
// A zero Amount captures the full authorized amount; a positive Amount captures
// that much and releases the rest.
type PaymentCapture struct {
	PaymentIntentRef string
	Amount           Money
	IdempotencyKey   string
}
