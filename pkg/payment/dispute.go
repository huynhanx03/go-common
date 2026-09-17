package payment

// DisputeStatus is the neutral lifecycle state of a dispute (chargeback).
type DisputeStatus string

const (
	DisputeStatusWarningNeedsResponse DisputeStatus = "warning_needs_response"
	DisputeStatusWarningUnderReview   DisputeStatus = "warning_under_review"
	DisputeStatusWarningClosed        DisputeStatus = "warning_closed"
	DisputeStatusNeedsResponse        DisputeStatus = "needs_response"
	DisputeStatusUnderReview          DisputeStatus = "under_review"
	DisputeStatusWon                  DisputeStatus = "won"
	DisputeStatusLost                 DisputeStatus = "lost"
)

// DisputeReason is the neutral reason a cardholder opened a dispute.
type DisputeReason string

const (
	DisputeReasonFraudulent           DisputeReason = "fraudulent"
	DisputeReasonDuplicate            DisputeReason = "duplicate"
	DisputeReasonProductNotReceived   DisputeReason = "product_not_received"
	DisputeReasonProductUnacceptable  DisputeReason = "product_unacceptable"
	DisputeReasonCreditNotProcessed   DisputeReason = "credit_not_processed"
	DisputeReasonSubscriptionCanceled DisputeReason = "subscription_canceled"
	DisputeReasonGeneral              DisputeReason = "general"
)

// Dispute is the neutral projection of a dispute (chargeback) raised against a
// charge. Disputes are learned about through webhooks (the charge.dispute.*
// event kinds); decode the event payload with EventDecoder.DecodeDispute.
type Dispute struct {
	Ref              string
	ChargeRef        string
	PaymentIntentRef string
	Status           DisputeStatus
	Reason           DisputeReason
	Amount           Money
}

// Lost reports whether the dispute was resolved against the merchant.
func (d *Dispute) Lost() bool {
	return d != nil && d.Status == DisputeStatusLost
}

// Won reports whether the dispute was resolved in the merchant's favor.
func (d *Dispute) Won() bool {
	return d != nil && d.Status == DisputeStatusWon
}
