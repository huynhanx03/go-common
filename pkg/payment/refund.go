package payment

// RefundStatus is the neutral status of a refund.
type RefundStatus string

const (
	RefundStatusPending        RefundStatus = "pending"
	RefundStatusSucceeded      RefundStatus = "succeeded"
	RefundStatusFailed         RefundStatus = "failed"
	RefundStatusCanceled       RefundStatus = "canceled"
	RefundStatusRequiresAction RefundStatus = "requires_action"
)

// Refund is the neutral projection of a refund against a payment intent.
type Refund struct {
	Ref              string
	PaymentIntentRef string
	Status           RefundStatus
	Amount           Money
	FailureReason    string
}

// RefundReason is the neutral reason a refund is issued.
type RefundReason int

const (
	RefundReasonRequestedByCustomer RefundReason = iota + 1
	RefundReasonDuplicate
	RefundReasonFraudulent
)

// String returns the provider-neutral wire token for the reason, or "" when the
// reason is not a defined value (which the adapter omits from the request).
func (r RefundReason) String() string {
	switch r {
	case RefundReasonRequestedByCustomer:
		return "requested_by_customer"
	case RefundReasonDuplicate:
		return "duplicate"
	case RefundReasonFraudulent:
		return "fraudulent"
	default:
		return ""
	}
}

// ParseRefundReason is the inverse of String; ok is false for unknown input.
func ParseRefundReason(v string) (RefundReason, bool) {
	switch v {
	case "requested_by_customer":
		return RefundReasonRequestedByCustomer, true
	case "duplicate":
		return RefundReasonDuplicate, true
	case "fraudulent":
		return RefundReasonFraudulent, true
	default:
		return 0, false
	}
}

// FullRefund refunds a payment intent in full. An unset Reason is omitted.
type FullRefund struct {
	PaymentIntentRef string
	Reason           RefundReason
	IdempotencyKey   string
}

// PartialRefund refunds a specific amount of a payment intent. Amount must be a
// positive minor-unit value no greater than the captured amount; refunding the
// whole amount is better expressed with FullRefund.
type PartialRefund struct {
	PaymentIntentRef string
	Amount           Money
	Reason           RefundReason
	IdempotencyKey   string
}
