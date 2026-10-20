package stripe

import (
	"context"
	"strings"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

const taxBehaviorInclusive = "inclusive"

// FetchInvoice retrieves an invoice.
func (p *Provider) FetchInvoice(ctx context.Context, ref string) (*payment.Invoice, *apperr.AppError) {
	return p.retrieveInvoice(ctx, ref, nil)
}

// FetchInvoiceWithConfirmationSecret retrieves an invoice with the client
// confirmation secret and payment references expanded, ready to confirm.
func (p *Provider) FetchInvoiceWithConfirmationSecret(ctx context.Context, ref string) (*payment.Invoice, *apperr.AppError) {
	params := &stripesdk.InvoiceRetrieveParams{}
	params.AddExpand("confirmation_secret")
	params.AddExpand("payments.data.payment")

	return p.retrieveInvoice(ctx, ref, params)
}

// ListOpenInvoices lists a subscription's open invoices.
func (p *Provider) ListOpenInvoices(ctx context.Context, in *payment.OpenInvoiceQuery) ([]payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil open invoice query")
	}

	params := &stripesdk.InvoiceListParams{
		Subscription: stripesdk.String(in.SubscriptionRef),
		Status:       stripesdk.String(string(stripesdk.InvoiceStatusOpen)),
	}

	if in.Limit > 0 {
		params.Limit = stripesdk.Int64(in.Limit)
	}

	out := make([]payment.Invoice, 0, in.Limit)

	for live, err := range p.client.V1Invoices.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, p.fail("stripe invoice list failed", err)
		}

		if mapped := invoiceToNeutral(live); mapped != nil {
			out = append(out, *mapped)
		}
	}

	return out, nil
}

// PreviewUpgradeCharge previews the invoice a plan change would raise.
func (p *Provider) PreviewUpgradeCharge(ctx context.Context, in *payment.PlanChangePreview) (*payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil plan change preview")
	}

	return p.createInvoicePreview(ctx, planChangePreviewParams(in), "stripe invoice preview (upgrade) failed")
}

// PreviewPlanChangeLines previews a plan change with per-line detail expanded.
func (p *Provider) PreviewPlanChangeLines(ctx context.Context, in *payment.PlanChangePreview) (*payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil plan change preview")
	}

	params := planChangePreviewParams(in)
	expandInvoiceLines(params)

	return p.createInvoicePreview(ctx, params, "stripe invoice preview failed")
}

// PreviewUpcomingInvoice previews a subscription's next scheduled invoice.
func (p *Provider) PreviewUpcomingInvoice(ctx context.Context, in *payment.UpcomingInvoicePreview) (*payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil upcoming invoice preview")
	}

	params := &stripesdk.InvoiceCreatePreviewParams{
		Subscription: stripesdk.String(in.SubscriptionRef),
	}

	if in.CustomerAccountRef != "" {
		params.CustomerAccount = stripesdk.String(in.CustomerAccountRef)
	}

	expandInvoiceLines(params)

	return p.createInvoicePreview(ctx, params, "stripe upcoming invoice preview failed")
}

// PayOutOfBand marks an invoice paid outside the provider (e.g. a bank transfer
// settled elsewhere).
func (p *Provider) PayOutOfBand(ctx context.Context, in *payment.InvoiceRef) (*payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil invoice request")
	}

	params := &stripesdk.InvoicePayParams{PaidOutOfBand: stripesdk.Bool(true)}
	setIdempotency(&params.Params, in.IdempotencyKey)

	paid, err := p.client.V1Invoices.Pay(ctx, in.InvoiceRef, params)
	if err != nil {
		return nil, p.fail("stripe invoice pay out of band failed", err)
	}

	return invoiceToNeutral(paid), nil
}

// Void voids an open invoice.
func (p *Provider) Void(ctx context.Context, in *payment.InvoiceRef) (*payment.Invoice, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil invoice request")
	}

	params := &stripesdk.InvoiceVoidInvoiceParams{}
	setIdempotency(&params.Params, in.IdempotencyKey)

	voided, err := p.client.V1Invoices.VoidInvoice(ctx, in.InvoiceRef, params)
	if err != nil {
		return nil, p.fail("stripe invoice void failed", err)
	}

	return invoiceToNeutral(voided), nil
}

func (p *Provider) retrieveInvoice(ctx context.Context, ref string, params *stripesdk.InvoiceRetrieveParams) (*payment.Invoice, *apperr.AppError) {
	live, err := p.client.V1Invoices.Retrieve(ctx, ref, params)
	if err != nil {
		return nil, p.fail("stripe invoice retrieve failed", err)
	}

	return invoiceToNeutral(live), nil
}

func (p *Provider) createInvoicePreview(ctx context.Context, params *stripesdk.InvoiceCreatePreviewParams, logMsg string) (*payment.Invoice, *apperr.AppError) {
	preview, err := p.client.V1Invoices.CreatePreview(ctx, params)
	if err != nil {
		return nil, p.fail(logMsg, err)
	}

	return invoiceToNeutral(preview), nil
}

func planChangePreviewParams(in *payment.PlanChangePreview) *stripesdk.InvoiceCreatePreviewParams {
	params := &stripesdk.InvoiceCreatePreviewParams{
		Subscription: stripesdk.String(in.SubscriptionRef),
		SubscriptionDetails: &stripesdk.InvoiceCreatePreviewSubscriptionDetailsParams{
			Items: []*stripesdk.InvoiceCreatePreviewSubscriptionDetailsItemParams{
				{
					ID:    stripesdk.String(in.ItemRef),
					Price: stripesdk.String(in.PriceRef),
				},
			},
			ProrationBehavior: stripesdk.String(payment.ProrationAlwaysInvoice.String()),
		},
	}

	if in.ProrationDate != nil && !in.ProrationDate.IsZero() {
		params.SubscriptionDetails.ProrationDate = stripesdk.Int64(in.ProrationDate.Unix())
	}

	if in.CustomerAccountRef != "" {
		params.CustomerAccount = stripesdk.String(in.CustomerAccountRef)
	}

	return params
}

func expandInvoiceLines(params *stripesdk.InvoiceCreatePreviewParams) {
	params.AddExpand("lines.data.parent.subscription_item_details")
	params.AddExpand("lines.data.parent.invoice_item_details")
}

func invoiceToNeutral(src *stripesdk.Invoice) *payment.Invoice {
	if src == nil {
		return nil
	}

	out := &payment.Invoice{
		Ref:                  src.ID,
		CustomerAccountRef:   src.CustomerAccount,
		SubscriptionRef:      invoiceSubscriptionRef(src),
		PaymentIntentRef:     invoicePaymentIntentRef(src),
		Status:               payment.InvoiceStatus(src.Status),
		BillingReason:        payment.InvoiceBillingReason(src.BillingReason),
		AmountDue:            moneyOf(src.AmountDue, src.Currency),
		AmountPaid:           moneyOf(src.AmountPaid, src.Currency),
		Subtotal:             moneyOf(src.Subtotal, src.Currency),
		Total:                moneyOf(src.Total, src.Currency),
		TotalExcludingTax:    moneyOf(src.TotalExcludingTax, src.Currency),
		SubscriptionMetadata: invoiceSubscriptionMetadata(src),
		Created:              unixPtrOrNil(src.Created),
		PeriodEnd:            unixPtrOrNil(src.PeriodEnd),
		PDFURL:               strings.TrimSpace(src.InvoicePDF),
		Lines:                invoiceLinesToNeutral(src.Lines),
	}

	out.AutomaticTax = src.AutomaticTax != nil && src.AutomaticTax.Enabled
	out.Tax, out.TaxBreakdown = invoiceTaxToNeutral(src)

	if src.Customer != nil {
		out.CustomerRef = src.Customer.ID
	}

	if src.ConfirmationSecret != nil {
		out.ConfirmationSecret = strings.TrimSpace(src.ConfirmationSecret.ClientSecret)
	}

	if src.StatusTransitions != nil {
		out.PaidAt = unixPtrOrNil(src.StatusTransitions.PaidAt)
	}

	return out
}

// invoiceTaxToNeutral sums the invoice's total taxes and returns the total plus
// the per-jurisdiction breakdown.
func invoiceTaxToNeutral(src *stripesdk.Invoice) (payment.Money, []payment.TaxAmount) {
	if len(src.TotalTaxes) == 0 {
		return payment.NewMoney(0, string(src.Currency)), nil
	}

	breakdown := make([]payment.TaxAmount, 0, len(src.TotalTaxes))
	var total int64

	for _, tax := range src.TotalTaxes {
		if tax == nil {
			continue
		}

		total += tax.Amount
		breakdown = append(breakdown, payment.TaxAmount{
			Amount:        moneyOf(tax.Amount, src.Currency),
			TaxableAmount: moneyOf(tax.TaxableAmount, src.Currency),
			Inclusive:     string(tax.TaxBehavior) == taxBehaviorInclusive,
			TaxRateRef:    invoiceTotalTaxRateRef(tax),
		})
	}

	return payment.NewMoney(total, string(src.Currency)), breakdown
}

func invoiceTotalTaxRateRef(tax *stripesdk.InvoiceTotalTax) string {
	if tax.TaxRateDetails == nil {
		return ""
	}

	return tax.TaxRateDetails.TaxRate
}

func invoiceSubscriptionMetadata(src *stripesdk.Invoice) map[string]string {
	if src.Parent == nil || src.Parent.SubscriptionDetails == nil {
		return nil
	}

	return src.Parent.SubscriptionDetails.Metadata
}

func invoiceSubscriptionRef(src *stripesdk.Invoice) string {
	if src.Parent == nil || src.Parent.SubscriptionDetails == nil || src.Parent.SubscriptionDetails.Subscription == nil {
		return ""
	}

	return strings.TrimSpace(src.Parent.SubscriptionDetails.Subscription.ID)
}

func invoicePaymentIntentRef(src *stripesdk.Invoice) string {
	if src.Payments == nil {
		return ""
	}

	for _, pay := range src.Payments.Data {
		if pay == nil || pay.Payment == nil || pay.Payment.PaymentIntent == nil {
			continue
		}

		if ref := strings.TrimSpace(pay.Payment.PaymentIntent.ID); ref != "" {
			return ref
		}
	}

	return ""
}

func invoiceLinesToNeutral(src *stripesdk.InvoiceLineItemList) []payment.InvoiceLine {
	if src == nil || len(src.Data) == 0 {
		return nil
	}

	out := make([]payment.InvoiceLine, 0, len(src.Data))

	for _, line := range src.Data {
		if line == nil {
			continue
		}

		out = append(out, invoiceLineToNeutral(line))
	}

	return out
}

func invoiceLineToNeutral(src *stripesdk.InvoiceLineItem) payment.InvoiceLine {
	out := payment.InvoiceLine{
		Description: src.Description,
		Amount:      moneyOf(src.Amount, src.Currency),
		Proration:   invoiceLineIsProration(src),
		TaxAmounts:  invoiceLineTaxesToNeutral(src),
	}

	if src.Period != nil {
		out.PeriodStart = unixPtrOrNil(src.Period.Start)
		out.PeriodEnd = unixPtrOrNil(src.Period.End)
	}

	if src.Pricing != nil && src.Pricing.PriceDetails != nil && src.Pricing.PriceDetails.Price != nil {
		out.PriceRef = src.Pricing.PriceDetails.Price.ID
	}

	return out
}

func invoiceLineTaxesToNeutral(src *stripesdk.InvoiceLineItem) []payment.TaxAmount {
	if len(src.Taxes) == 0 {
		return nil
	}

	out := make([]payment.TaxAmount, 0, len(src.Taxes))

	for _, tax := range src.Taxes {
		if tax == nil {
			continue
		}

		rateRef := ""
		if tax.TaxRateDetails != nil {
			rateRef = tax.TaxRateDetails.TaxRate
		}

		out = append(out, payment.TaxAmount{
			Amount:        moneyOf(tax.Amount, src.Currency),
			TaxableAmount: moneyOf(tax.TaxableAmount, src.Currency),
			Inclusive:     string(tax.TaxBehavior) == taxBehaviorInclusive,
			TaxRateRef:    rateRef,
		})
	}

	return out
}

func invoiceLineIsProration(src *stripesdk.InvoiceLineItem) bool {
	if src.Parent == nil {
		return false
	}

	if src.Parent.SubscriptionItemDetails != nil && src.Parent.SubscriptionItemDetails.Proration {
		return true
	}

	return src.Parent.InvoiceItemDetails != nil && src.Parent.InvoiceItemDetails.Proration
}
