package payment

// CustomerAccount is the neutral projection of the provider-side account a
// subscription and its payment methods hang off.
type CustomerAccount struct {
	Ref                     string
	DefaultPaymentMethodRef string
}

// NewCustomerAccount creates a customer account with the provider.
//
// It does not carry a customer address: the Stripe reference driver requests the
// automatic-indirect-tax capability, and for automatic tax the customer location
// is supplied by the flow that has it — hosted Checkout collects the address in
// the session. (A per-customer address seed is intentionally omitted; see the
// driver's CreateCustomerAccount for why the V2 address path is not used.)
type NewCustomerAccount struct {
	ContactEmail   string
	DisplayName    string
	Metadata       map[string]string
	IdempotencyKey string
}

// CustomerDefaultPaymentMethodChange sets the account-level default payment
// method used when a specific one is not named.
type CustomerDefaultPaymentMethodChange struct {
	CustomerAccountRef string
	PaymentMethodRef   string
	IdempotencyKey     string
}
