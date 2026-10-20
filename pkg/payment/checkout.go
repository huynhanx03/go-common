package payment

// CheckoutStatus is the neutral status of a hosted checkout session.
type CheckoutStatus string

const (
	CheckoutStatusOpen     CheckoutStatus = "open"
	CheckoutStatusComplete CheckoutStatus = "complete"
	CheckoutStatusExpired  CheckoutStatus = "expired"
)

// CheckoutPaymentStatus is the neutral payment state of a checkout session.
type CheckoutPaymentStatus string

const (
	CheckoutPaymentStatusPaid              CheckoutPaymentStatus = "paid"
	CheckoutPaymentStatusUnpaid            CheckoutPaymentStatus = "unpaid"
	CheckoutPaymentStatusNoPaymentRequired CheckoutPaymentStatus = "no_payment_required"
)

// Checkout is the neutral projection of a hosted checkout session.
type Checkout struct {
	Ref                string
	ClientSecret       string
	Status             CheckoutStatus
	PaymentStatus      CheckoutPaymentStatus
	CustomerRef        string
	CustomerAccountRef string
	SubscriptionRef    string
	InvoiceRef         string
	PaymentIntentRef   string
	ClientReferenceID  string
	Metadata           map[string]string
	AmountTotal        Money
	AmountTax          Money
}

// Confirmed reports whether the session finished successfully.
func (c *Checkout) Confirmed() bool {
	return c != nil && c.Status == CheckoutStatusComplete
}

// Discount applies a coupon or a promotion code to a checkout. Set exactly one
// of the fields. A checkout may carry Discounts or allow the customer to enter a
// promotion code (AllowPromotionCodes), but not both.
type Discount struct {
	CouponRef        string
	PromotionCodeRef string
}

// CheckoutLineItem is one catalog-priced line of a subscription checkout.
type CheckoutLineItem struct {
	PriceRef string
	Quantity int64
}

// OneTimeLineItem is one ad-hoc priced line of a one-time checkout. Its price is
// defined inline rather than referencing the catalog.
type OneTimeLineItem struct {
	Amount      Money
	ProductName string
	Quantity    int64
}

// SubscriptionCheckout opens a hosted checkout that starts a subscription from
// one or more catalog prices. When AutomaticTax is set, the provider computes
// tax from the customer's location.
type SubscriptionCheckout struct {
	CustomerAccountRef  string
	LineItems           []CheckoutLineItem
	ReturnURL           string
	ClientReferenceID   string
	AutomaticTax        bool
	AllowPromotionCodes bool
	Discounts           []Discount
	Metadata            map[string]string
	IdempotencyKey      string
}

// OneTimeCheckout opens a hosted checkout for one or more ad-hoc line items.
// When AutomaticTax is set, the provider computes tax from the customer's
// location.
type OneTimeCheckout struct {
	CustomerAccountRef  string
	LineItems           []OneTimeLineItem
	ReturnURL           string
	ClientReferenceID   string
	AutomaticTax        bool
	AllowPromotionCodes bool
	Discounts           []Discount
	Metadata            map[string]string
	IdempotencyKey      string
}
