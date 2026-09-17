package payment

import "time"

// EventKind is the neutral type of a provider webhook event. The values mirror
// the common Stripe-style dotted names; a provider that emits a kind outside
// this set has it preserved verbatim.
type EventKind string

const (
	EventKindCheckoutSessionCompleted     EventKind = "checkout.session.completed"
	EventKindCheckoutSessionAsyncFailed   EventKind = "checkout.session.async_payment_failed"
	EventKindCheckoutSessionExpired       EventKind = "checkout.session.expired"
	EventKindCustomerSubscriptionCreated  EventKind = "customer.subscription.created"
	EventKindCustomerSubscriptionUpdated  EventKind = "customer.subscription.updated"
	EventKindCustomerSubscriptionDeleted  EventKind = "customer.subscription.deleted"
	EventKindSubscriptionScheduleReleased EventKind = "subscription_schedule.released"
	EventKindSubscriptionScheduleCanceled EventKind = "subscription_schedule.canceled"
	EventKindInvoiceCreated               EventKind = "invoice.created"
	EventKindInvoiceFinalized             EventKind = "invoice.finalized"
	EventKindInvoicePaid                  EventKind = "invoice.paid"
	EventKindInvoicePaymentFailed         EventKind = "invoice.payment_failed"
	EventKindPaymentIntentSucceeded       EventKind = "payment_intent.succeeded"
	EventKindPaymentIntentPaymentFailed   EventKind = "payment_intent.payment_failed"
	EventKindPaymentIntentCanceled        EventKind = "payment_intent.canceled"
	EventKindSetupIntentSucceeded         EventKind = "setup_intent.succeeded"
	EventKindChargeDisputeCreated         EventKind = "charge.dispute.created"
	EventKindChargeDisputeUpdated         EventKind = "charge.dispute.updated"
	EventKindChargeDisputeClosed          EventKind = "charge.dispute.closed"
	EventKindChargeDisputeFundsWithdrawn  EventKind = "charge.dispute.funds_withdrawn"
	EventKindChargeDisputeFundsReinstated EventKind = "charge.dispute.funds_reinstated"
	EventKindProductCreated               EventKind = "product.created"
	EventKindProductUpdated               EventKind = "product.updated"
	EventKindProductDeleted               EventKind = "product.deleted"
	EventKindPriceCreated                 EventKind = "price.created"
	EventKindPriceUpdated                 EventKind = "price.updated"
	EventKindPriceDeleted                 EventKind = "price.deleted"
)

// Event is the verified, decoded envelope of a provider webhook. Payload is the
// raw provider object bytes, which the matching EventDecoder Decode* method
// turns into a neutral type.
type Event struct {
	ID         string
	Kind       EventKind
	OccurredAt time.Time
	Payload    []byte
}
