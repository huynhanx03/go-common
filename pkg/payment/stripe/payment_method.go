package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

const paymentMethodPageSize = 100

// ListPaymentMethods lists the payment methods stored on a customer account.
func (p *Provider) ListPaymentMethods(ctx context.Context, customerAccountRef string) ([]payment.PaymentMethod, *apperr.AppError) {
	params := &stripesdk.PaymentMethodListParams{CustomerAccount: stripesdk.String(customerAccountRef)}
	params.Limit = stripesdk.Int64(paymentMethodPageSize)

	methods := make([]payment.PaymentMethod, 0)

	for method, err := range p.client.V1PaymentMethods.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, p.fail("stripe payment methods list failed", err)
		}

		if method == nil || method.ID == "" {
			continue
		}

		methods = append(methods, paymentMethodToNeutral(method))
	}

	return methods, nil
}

// Detach removes a stored payment method from its customer.
func (p *Provider) Detach(ctx context.Context, in *payment.PaymentMethodDetach) (*payment.PaymentMethod, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil payment method detach request")
	}

	params := &stripesdk.PaymentMethodDetachParams{}
	setIdempotency(&params.Params, in.IdempotencyKey)

	detached, err := p.client.V1PaymentMethods.Detach(ctx, in.PaymentMethodRef, params)
	if err != nil {
		return nil, p.fail("stripe payment method detach failed", err)
	}

	if detached == nil {
		return nil, payment.ProviderContractBroken("stripe returned no payment method")
	}

	out := paymentMethodToNeutral(detached)

	return &out, nil
}

// UpdatePaymentMethod edits a stored card's expiry and cardholder name.
func (p *Provider) UpdatePaymentMethod(ctx context.Context, in *payment.PaymentMethodUpdate) (*payment.PaymentMethod, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil payment method update request")
	}

	params := &stripesdk.PaymentMethodUpdateParams{}
	setIdempotency(&params.Params, in.IdempotencyKey)

	if in.ExpMonth > 0 && in.ExpYear > 0 {
		params.Card = &stripesdk.PaymentMethodUpdateCardParams{
			ExpMonth: stripesdk.Int64(in.ExpMonth),
			ExpYear:  stripesdk.Int64(in.ExpYear),
		}
	}

	if in.CardholderName != "" {
		params.BillingDetails = &stripesdk.PaymentMethodUpdateBillingDetailsParams{
			Name: stripesdk.String(in.CardholderName),
		}
	}

	updated, err := p.client.V1PaymentMethods.Update(ctx, in.PaymentMethodRef, params)
	if err != nil {
		return nil, p.fail("stripe payment method update failed", err)
	}

	if updated == nil {
		return nil, payment.ProviderContractBroken("stripe returned no payment method")
	}

	out := paymentMethodToNeutral(updated)

	return &out, nil
}

func paymentMethodToNeutral(src *stripesdk.PaymentMethod) payment.PaymentMethod {
	out := payment.PaymentMethod{
		Ref:       src.ID,
		Type:      payment.PaymentMethodType(src.Type),
		BankDebit: bankDebitToNeutral(src),
	}

	if src.Card != nil {
		out.Card = &payment.Card{
			Brand:       string(src.Card.Brand),
			Last4:       src.Card.Last4,
			ExpMonth:    src.Card.ExpMonth,
			ExpYear:     src.Card.ExpYear,
			Fingerprint: src.Card.Fingerprint,
		}

		if src.BillingDetails != nil {
			out.Card.CardholderName = src.BillingDetails.Name
		}
	}

	return out
}

// bankDebitToNeutral picks the populated debit rail. The order is the precedence
// used to label a method when more than one rail is present.
func bankDebitToNeutral(src *stripesdk.PaymentMethod) *payment.BankDebit {
	switch {
	case src.USBankAccount != nil && src.USBankAccount.Last4 != "":
		return &payment.BankDebit{Rail: payment.PaymentMethodTypeUSBankAccount, Last4: src.USBankAccount.Last4}
	case src.SEPADebit != nil && src.SEPADebit.Last4 != "":
		return &payment.BankDebit{Rail: payment.PaymentMethodTypeSEPADebit, Last4: src.SEPADebit.Last4}
	case src.BACSDebit != nil && src.BACSDebit.Last4 != "":
		return &payment.BankDebit{Rail: payment.PaymentMethodTypeBACSDebit, Last4: src.BACSDebit.Last4}
	case src.AUBECSDebit != nil && src.AUBECSDebit.Last4 != "":
		return &payment.BankDebit{Rail: payment.PaymentMethodTypeAUBECSDebit, Last4: src.AUBECSDebit.Last4}
	case src.ACSSDebit != nil && src.ACSSDebit.Last4 != "":
		return &payment.BankDebit{Rail: payment.PaymentMethodTypeACSSDebit, Last4: src.ACSSDebit.Last4}
	default:
		return nil
	}
}
