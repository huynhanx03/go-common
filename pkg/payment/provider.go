package payment

// Provider is the minimal handle the registry hands back. A concrete provider
// implements whichever capability interfaces it supports, so a rail that has no
// subscriptions is still a valid Provider. Discover what a provider can do with
// the capability helpers below, which type-assert to the narrow interfaces.
type Provider interface {
	// Name is the provider's registry key, e.g. "stripe".
	Name() string
}

// The helpers each report whether the provider implements one capability. They
// read as intent at the call site and give a clean (value, ok) pair:
//
//	subs, ok := payment.Subscriptions(p)
//	if !ok { /* this provider has no subscriptions */ }

// Subscriptions returns the provider's SubscriptionGateway, if supported.
func Subscriptions(p Provider) (SubscriptionGateway, bool) {
	g, ok := p.(SubscriptionGateway)
	return g, ok
}

// CustomerAccounts returns the provider's CustomerAccountGateway, if supported.
func CustomerAccounts(p Provider) (CustomerAccountGateway, bool) {
	g, ok := p.(CustomerAccountGateway)
	return g, ok
}

// Invoices returns the provider's InvoiceGateway, if supported.
func Invoices(p Provider) (InvoiceGateway, bool) {
	g, ok := p.(InvoiceGateway)
	return g, ok
}

// PaymentIntents returns the provider's PaymentIntentGateway, if supported.
func PaymentIntents(p Provider) (PaymentIntentGateway, bool) {
	g, ok := p.(PaymentIntentGateway)
	return g, ok
}

// Schedules returns the provider's ScheduleGateway, if supported.
func Schedules(p Provider) (ScheduleGateway, bool) {
	g, ok := p.(ScheduleGateway)
	return g, ok
}

// Events returns the provider's EventDecoder, if supported.
func Events(p Provider) (EventDecoder, bool) {
	g, ok := p.(EventDecoder)
	return g, ok
}

// Checkouts returns the provider's CheckoutGateway, if supported.
func Checkouts(p Provider) (CheckoutGateway, bool) {
	g, ok := p.(CheckoutGateway)
	return g, ok
}

// Catalog returns the provider's CatalogGateway, if supported.
func Catalog(p Provider) (CatalogGateway, bool) {
	g, ok := p.(CatalogGateway)
	return g, ok
}

// PaymentMethods returns the provider's PaymentMethodGateway, if supported.
func PaymentMethods(p Provider) (PaymentMethodGateway, bool) {
	g, ok := p.(PaymentMethodGateway)
	return g, ok
}

// SetupIntents returns the provider's SetupIntentGateway, if supported.
func SetupIntents(p Provider) (SetupIntentGateway, bool) {
	g, ok := p.(SetupIntentGateway)
	return g, ok
}

// Refunds returns the provider's RefundGateway, if supported.
func Refunds(p Provider) (RefundGateway, bool) {
	g, ok := p.(RefundGateway)
	return g, ok
}
