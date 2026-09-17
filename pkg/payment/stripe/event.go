package stripe

import (
	"fmt"
	"strings"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
	"github.com/stripe/stripe-go/v85/webhook"
)

// VerifyAndDecode authenticates a webhook delivery against the configured
// signing secret and returns the neutral event envelope.
func (p *Provider) VerifyAndDecode(payload []byte, signatureHeader string) (*payment.Event, *apperr.AppError) {
	if signatureHeader == "" {
		return nil, payment.Invalid("missing Stripe-Signature header")
	}

	if p.webhookSecret == "" {
		p.logError("stripe webhook secret not configured", nil)

		return nil, providerErr("webhook not configured")
	}

	event, err := webhook.ConstructEventWithOptions(payload, signatureHeader, p.webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		p.logWarn("stripe webhook signature verification failed", err)

		return nil, payment.Invalid("signature verification failed")
	}

	if !compatibleAPIVersion(stripesdk.APIVersion, event.APIVersion) {
		p.logError(fmt.Sprintf(
			"stripe webhook endpoint is pinned to API version %q but this build expects %q: "+
				"every delivery is rejected until the endpoint is repinned in the Stripe Dashboard",
			event.APIVersion, stripesdk.APIVersion), nil)

		return nil, providerErr("webhook api version mismatch")
	}

	return eventToNeutral(event), nil
}

// DecodeSubscription decodes a verified subscription event payload.
func (p *Provider) DecodeSubscription(payload []byte) (*payment.Subscription, *apperr.AppError) {
	var src stripesdk.Subscription

	if appErr := unmarshalEventObject(payload, "subscription", &src); appErr != nil {
		return nil, appErr
	}

	out := subscriptionToNeutral(&src)
	applySubscriptionEventOnlyFields(out, payload)

	return out, nil
}

// DecodeInvoice decodes a verified invoice event payload.
func (p *Provider) DecodeInvoice(payload []byte) (*payment.Invoice, *apperr.AppError) {
	var src stripesdk.Invoice

	if appErr := unmarshalEventObject(payload, "invoice", &src); appErr != nil {
		return nil, appErr
	}

	out := invoiceToNeutral(&src)
	applyInvoiceEventOnlyFields(out, payload)

	return out, nil
}

// DecodePaymentIntent decodes a verified payment intent event payload.
func (p *Provider) DecodePaymentIntent(payload []byte) (*payment.PaymentIntent, *apperr.AppError) {
	var src stripesdk.PaymentIntent

	if appErr := unmarshalEventObject(payload, "payment_intent", &src); appErr != nil {
		return nil, appErr
	}

	return paymentIntentToNeutral(&src), nil
}

// DecodeSetupIntent decodes a verified setup intent event payload.
func (p *Provider) DecodeSetupIntent(payload []byte) (*payment.SetupIntent, *apperr.AppError) {
	var src stripesdk.SetupIntent

	if appErr := unmarshalEventObject(payload, "setup intent", &src); appErr != nil {
		return nil, appErr
	}

	return setupIntentToNeutral(&src), nil
}

// DecodeCheckout decodes a verified checkout session event payload.
func (p *Provider) DecodeCheckout(payload []byte) (*payment.Checkout, *apperr.AppError) {
	var src stripesdk.CheckoutSession

	if appErr := unmarshalEventObject(payload, "checkout session", &src); appErr != nil {
		return nil, appErr
	}

	return checkoutToNeutral(&src), nil
}

// DecodeSchedule decodes a verified subscription schedule event payload.
func (p *Provider) DecodeSchedule(payload []byte) (*payment.Schedule, *apperr.AppError) {
	var src stripesdk.SubscriptionSchedule

	if appErr := unmarshalEventObject(payload, "subscription schedule", &src); appErr != nil {
		return nil, appErr
	}

	return scheduleToNeutral(&src), nil
}

// DecodeProduct decodes a verified product event payload.
func (p *Provider) DecodeProduct(payload []byte) (*payment.Product, *apperr.AppError) {
	var src stripesdk.Product

	if appErr := unmarshalEventObject(payload, "product", &src); appErr != nil {
		return nil, appErr
	}

	out := productToNeutral(&src)

	return &out, nil
}

// DecodePrice decodes a verified price event payload.
func (p *Provider) DecodePrice(payload []byte) (*payment.Price, *apperr.AppError) {
	var src stripesdk.Price

	if appErr := unmarshalEventObject(payload, "price", &src); appErr != nil {
		return nil, appErr
	}

	out := priceToNeutral(&src)

	return &out, nil
}

// DecodeDispute decodes a verified dispute (chargeback) event payload.
func (p *Provider) DecodeDispute(payload []byte) (*payment.Dispute, *apperr.AppError) {
	var src stripesdk.Dispute

	if appErr := unmarshalEventObject(payload, "dispute", &src); appErr != nil {
		return nil, appErr
	}

	return disputeToNeutral(&src), nil
}

func disputeToNeutral(src *stripesdk.Dispute) *payment.Dispute {
	if src == nil {
		return nil
	}

	out := &payment.Dispute{
		Ref:    src.ID,
		Status: payment.DisputeStatus(src.Status),
		Reason: payment.DisputeReason(src.Reason),
		Amount: moneyOf(src.Amount, src.Currency),
	}

	if src.Charge != nil {
		out.ChargeRef = src.Charge.ID
	}

	if src.PaymentIntent != nil {
		out.PaymentIntentRef = src.PaymentIntent.ID
	}

	return out
}

func providerErr(message string) *apperr.AppError {
	return apperr.New(payment.CodePaymentProviderError, message, nil)
}

func eventToNeutral(src stripesdk.Event) *payment.Event {
	out := &payment.Event{
		ID:         src.ID,
		Kind:       payment.EventKind(src.Type),
		OccurredAt: time.Unix(src.Created, 0).UTC(),
	}

	if src.Data != nil {
		out.Payload = src.Data.Raw
	}

	return out
}

// unmarshalEventObject decodes a verified payload with go-common's JSON codec.
// A malformed object is ours to retry: the signature already proved the sender,
// so the only remaining cause is a shape this build cannot read.
func unmarshalEventObject(payload []byte, resource string, into any) *apperr.AppError {
	if err := json.Unmarshal(payload, into); err != nil {
		return providerErr("failed to parse " + resource + " event: " + err.Error())
	}

	return nil
}

func compatibleAPIVersion(sdkVersion, eventVersion string) bool {
	sdkTrain, ok := releaseTrain(sdkVersion)
	if !ok {
		return false
	}

	eventTrain, ok := releaseTrain(eventVersion)
	if !ok {
		return false
	}

	if sdkTrain == "preview" {
		return sdkVersion == eventVersion
	}

	return eventTrain == sdkTrain
}

func releaseTrain(version string) (string, bool) {
	_, train, found := strings.Cut(version, ".")

	return train, found && train != ""
}

// The following read fields that appear only on the raw event payload, not on a
// freshly retrieved object. They use gjson path extraction so no throwaway
// struct is decoded just to reach one field.

// applySubscriptionEventOnlyFields fills the plan price and product refs.
func applySubscriptionEventOnlyFields(out *payment.Subscription, payload []byte) {
	if id, ok := json.GetString(payload, "plan.id"); ok {
		out.PlanPriceRef = strings.TrimSpace(id)
	}

	out.PlanProductRef = expandableRef(payload, "plan.product")
}

func applyInvoiceEventOnlyFields(out *payment.Invoice, payload []byte) {
	if ref, ok := json.GetString(payload, "subscription"); ok {
		if ref = strings.TrimSpace(ref); ref != "" {
			out.SubscriptionRef = ref
		}
	}

	if ref := expandableRef(payload, "payment_intent"); ref != "" {
		out.PaymentIntentRef = ref
	}

	if json.Exists(payload, "last_payment_error") {
		code, _ := json.GetString(payload, "last_payment_error.code")
		declineCode, _ := json.GetString(payload, "last_payment_error.decline_code")
		message, _ := json.GetString(payload, "last_payment_error.message")

		out.LastPaymentError = &payment.PaymentError{
			Code:        code,
			DeclineCode: declineCode,
			Message:     message,
		}
	}

	applyInvoiceLinePriceRefs(out, payload)
}

// applyInvoiceLinePriceRefs backfills each line's price ref from the payload,
// only when the payload's line count matches the mapped lines so indexes align.
func applyInvoiceLinePriceRefs(out *payment.Invoice, payload []byte) {
	refs := make([]string, 0, len(out.Lines))

	json.ForEach(payload, "lines.data", func(_ string, line []byte) bool {
		refs = append(refs, expandableRef(line, "price"))
		return true
	})

	if len(refs) != len(out.Lines) {
		return
	}

	for i, ref := range refs {
		if ref != "" {
			out.Lines[i].PriceRef = ref
		}
	}
}

// expandableRef resolves a Stripe "expandable" field at path, which is either a
// bare string ID or an expanded object carrying an "id". It prefers the nested
// id so an expanded object is never mistaken for its raw JSON.
func expandableRef(data []byte, path string) string {
	if id, ok := json.GetString(data, path+".id"); ok && id != "" {
		return strings.TrimSpace(id)
	}

	if s, ok := json.GetString(data, path); ok && !strings.HasPrefix(strings.TrimSpace(s), "{") {
		return strings.TrimSpace(s)
	}

	return ""
}
