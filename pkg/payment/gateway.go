package payment

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

// The interfaces below are deliberately small (one capability each) so a
// service depends only on what it uses and a provider implements only what it
// supports. Every method takes a context.Context and returns a neutral type
// plus *apperr.AppError. Request structs are passed by pointer to avoid copying
// and to keep the contract uniform; adapters reject a nil request.

// SubscriptionGateway manages the lifecycle of recurring subscriptions.
type SubscriptionGateway interface {
	Fetch(ctx context.Context, ref string) (*Subscription, *apperr.AppError)
	FetchWithLatestInvoice(ctx context.Context, ref string) (*Subscription, *apperr.AppError)
	ChangePlanItem(ctx context.Context, in *PlanItemChange) (*Subscription, *apperr.AppError)
	UpgradeWithPendingInvoice(ctx context.Context, in *PlanItemChange) (*Subscription, *apperr.AppError)
	CancelAtPeriodEnd(ctx context.Context, in *SubscriptionRef) (*Subscription, *apperr.AppError)
	Resume(ctx context.Context, in *SubscriptionRef) (*Subscription, *apperr.AppError)
	CancelNow(ctx context.Context, in *SubscriptionRef) (*Subscription, *apperr.AppError)
	ResetBillingAnchorNow(ctx context.Context, in *SubscriptionRef) (*Subscription, *apperr.AppError)
	SetDefaultPaymentMethod(ctx context.Context, in *DefaultPaymentMethodChange) (*Subscription, *apperr.AppError)
}

// CustomerAccountGateway manages provider-side customer accounts.
type CustomerAccountGateway interface {
	CreateCustomerAccount(ctx context.Context, in *NewCustomerAccount) (*CustomerAccount, *apperr.AppError)
	FetchCustomerAccount(ctx context.Context, accountRef string) (*CustomerAccount, *apperr.AppError)
	SetAccountDefaultPaymentMethod(ctx context.Context, in *CustomerDefaultPaymentMethodChange) (*CustomerAccount, *apperr.AppError)
}

// InvoiceGateway reads invoices and runs invoice-level operations.
type InvoiceGateway interface {
	FetchInvoice(ctx context.Context, ref string) (*Invoice, *apperr.AppError)
	FetchInvoiceWithConfirmationSecret(ctx context.Context, ref string) (*Invoice, *apperr.AppError)
	ListOpenInvoices(ctx context.Context, in *OpenInvoiceQuery) ([]Invoice, *apperr.AppError)
	PreviewUpgradeCharge(ctx context.Context, in *PlanChangePreview) (*Invoice, *apperr.AppError)
	PreviewPlanChangeLines(ctx context.Context, in *PlanChangePreview) (*Invoice, *apperr.AppError)
	PreviewUpcomingInvoice(ctx context.Context, in *UpcomingInvoicePreview) (*Invoice, *apperr.AppError)
	PayOutOfBand(ctx context.Context, in *InvoiceRef) (*Invoice, *apperr.AppError)
	Void(ctx context.Context, in *InvoiceRef) (*Invoice, *apperr.AppError)
}

// PaymentIntentGateway manages one-off payment attempts.
type PaymentIntentGateway interface {
	FetchPaymentIntent(ctx context.Context, ref string) (*PaymentIntent, *apperr.AppError)
	CreateOneTimeCharge(ctx context.Context, in *OneTimeCharge) (*PaymentIntent, *apperr.AppError)
	CapturePayment(ctx context.Context, in *PaymentCapture) (*PaymentIntent, *apperr.AppError)
	CancelAbandoned(ctx context.Context, in *PaymentIntentRef) (*PaymentIntent, *apperr.AppError)
}

// ScheduleGateway manages subscription schedules.
type ScheduleGateway interface {
	CreateAtPeriodEnd(ctx context.Context, in *ScheduleAtPeriodEnd) (*Schedule, *apperr.AppError)
	Release(ctx context.Context, in *ScheduleRef) (*Schedule, *apperr.AppError)
}

// EventDecoder verifies and decodes provider webhooks. VerifyAndDecode
// authenticates the delivery and returns the neutral envelope; each Decode*
// turns an already-verified Event.Payload into a neutral object.
type EventDecoder interface {
	VerifyAndDecode(payload []byte, signatureHeader string) (*Event, *apperr.AppError)
	DecodeSubscription(payload []byte) (*Subscription, *apperr.AppError)
	DecodeInvoice(payload []byte) (*Invoice, *apperr.AppError)
	DecodePaymentIntent(payload []byte) (*PaymentIntent, *apperr.AppError)
	DecodeCheckout(payload []byte) (*Checkout, *apperr.AppError)
	DecodeSchedule(payload []byte) (*Schedule, *apperr.AppError)
	DecodeProduct(payload []byte) (*Product, *apperr.AppError)
	DecodePrice(payload []byte) (*Price, *apperr.AppError)
	DecodeSetupIntent(payload []byte) (*SetupIntent, *apperr.AppError)
	DecodeDispute(payload []byte) (*Dispute, *apperr.AppError)
}

// CheckoutGateway opens and reads hosted checkout sessions.
type CheckoutGateway interface {
	OpenSubscription(ctx context.Context, in *SubscriptionCheckout) (*Checkout, *apperr.AppError)
	OpenOneTimePayment(ctx context.Context, in *OneTimeCheckout) (*Checkout, *apperr.AppError)
	FetchCheckout(ctx context.Context, ref string) (*Checkout, *apperr.AppError)
}

// CatalogGateway reads the provider's product and price catalog.
type CatalogGateway interface {
	ListProducts(ctx context.Context, ids []string) ([]Product, *apperr.AppError)
	ListPrices(ctx context.Context) ([]Price, *apperr.AppError)
}

// PaymentMethodGateway manages stored payment methods.
type PaymentMethodGateway interface {
	ListPaymentMethods(ctx context.Context, customerAccountRef string) ([]PaymentMethod, *apperr.AppError)
	Detach(ctx context.Context, in *PaymentMethodDetach) (*PaymentMethod, *apperr.AppError)
	UpdatePaymentMethod(ctx context.Context, in *PaymentMethodUpdate) (*PaymentMethod, *apperr.AppError)
}

// SetupIntentGateway sets up off-session payment methods for later charges.
type SetupIntentGateway interface {
	CreateSetupIntent(ctx context.Context, in *NewSetupIntent) (*SetupIntent, *apperr.AppError)
}

// RefundGateway issues refunds.
type RefundGateway interface {
	RefundInFull(ctx context.Context, in *FullRefund) (*Refund, *apperr.AppError)
	RefundAmount(ctx context.Context, in *PartialRefund) (*Refund, *apperr.AppError)
}
