package payment

import "time"

// InvoiceStatus is the neutral invoice lifecycle state.
type InvoiceStatus string

const (
	InvoiceStatusDraft         InvoiceStatus = "draft"
	InvoiceStatusOpen          InvoiceStatus = "open"
	InvoiceStatusPaid          InvoiceStatus = "paid"
	InvoiceStatusUncollectible InvoiceStatus = "uncollectible"
	InvoiceStatusVoid          InvoiceStatus = "void"
)

// InvoiceBillingReason explains why the provider raised an invoice.
type InvoiceBillingReason string

const (
	InvoiceBillingReasonAutomaticPendingInvoiceItemInvoice InvoiceBillingReason = "automatic_pending_invoice_item_invoice"
	InvoiceBillingReasonManual                             InvoiceBillingReason = "manual"
	InvoiceBillingReasonQuoteAccept                        InvoiceBillingReason = "quote_accept"
	InvoiceBillingReasonSubscription                       InvoiceBillingReason = "subscription"
	InvoiceBillingReasonSubscriptionCreate                 InvoiceBillingReason = "subscription_create"
	InvoiceBillingReasonSubscriptionCycle                  InvoiceBillingReason = "subscription_cycle"
	InvoiceBillingReasonSubscriptionThreshold              InvoiceBillingReason = "subscription_threshold"
	InvoiceBillingReasonSubscriptionUpdate                 InvoiceBillingReason = "subscription_update"
	InvoiceBillingReasonUpcoming                           InvoiceBillingReason = "upcoming"
)

// Invoice is the neutral projection of a provider invoice. All monetary fields
// are currency-aware Money values.
type Invoice struct {
	Ref                string
	CustomerRef        string
	CustomerAccountRef string
	SubscriptionRef    string
	PaymentIntentRef   string
	Status             InvoiceStatus
	BillingReason      InvoiceBillingReason
	AmountDue          Money
	AmountPaid         Money
	Subtotal           Money
	Total              Money
	TotalExcludingTax  Money
	Created            *time.Time
	PeriodEnd          *time.Time
	PaidAt             *time.Time
	PDFURL             string
	ConfirmationSecret string

	// AutomaticTax reports whether the provider computed tax for this invoice
	// from the customer's location. Tax is the total tax charged, and
	// TaxBreakdown splits it by jurisdiction.
	AutomaticTax bool
	Tax          Money
	TaxBreakdown []TaxAmount

	SubscriptionMetadata map[string]string

	LastPaymentError *PaymentError
	Lines            []InvoiceLine
}

// InvoiceLine is one line of an invoice.
type InvoiceLine struct {
	Description string
	Amount      Money
	PriceRef    string

	Proration   bool
	PeriodStart *time.Time
	PeriodEnd   *time.Time

	// TaxAmounts is the per-jurisdiction tax computed for this line.
	TaxAmounts []TaxAmount
}

// ChargedPriceRef returns the price the invoice actually charges for: the first
// positive-amount line, falling back to the first line that names a price. It
// returns "" when no line names a price.
func (i *Invoice) ChargedPriceRef() string {
	if i == nil {
		return ""
	}

	fallback := ""

	for _, line := range i.Lines {
		if line.PriceRef == "" {
			continue
		}

		if line.Amount.Amount > 0 {
			return line.PriceRef
		}

		if fallback == "" {
			fallback = line.PriceRef
		}
	}

	return fallback
}

// ProrationTotals sums the proration credits and charges on the invoice, both
// returned as positive minor-unit amounts.
func (i *Invoice) ProrationTotals() (creditCents, chargeCents int64) {
	if i == nil {
		return 0, 0
	}

	for _, line := range i.Lines {
		if !line.Proration {
			continue
		}

		if line.Amount.Amount < 0 {
			creditCents += -line.Amount.Amount
		} else {
			chargeCents += line.Amount.Amount
		}
	}

	return creditCents, chargeCents
}

// InvoiceRef names an invoice for an idempotent mutation.
type InvoiceRef struct {
	InvoiceRef     string
	IdempotencyKey string
}

// OpenInvoiceQuery lists a subscription's open invoices, newest first, bounded
// by Limit (0 means the provider default).
type OpenInvoiceQuery struct {
	SubscriptionRef string
	Limit           int64
}

// PlanChangePreview previews the invoice a plan change would produce, without
// committing it.
type PlanChangePreview struct {
	SubscriptionRef    string
	ItemRef            string
	PriceRef           string
	CustomerAccountRef string

	ProrationDate *time.Time
}

// UpcomingInvoicePreview previews a subscription's next scheduled invoice.
type UpcomingInvoicePreview struct {
	SubscriptionRef    string
	CustomerAccountRef string
}
