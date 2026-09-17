package payment

// PaymentMethodType is the neutral kind of a stored payment method.
type PaymentMethodType string

const (
	PaymentMethodTypeCard          PaymentMethodType = "card"
	PaymentMethodTypeUSBankAccount PaymentMethodType = "us_bank_account"
	PaymentMethodTypeSEPADebit     PaymentMethodType = "sepa_debit"
	PaymentMethodTypeBACSDebit     PaymentMethodType = "bacs_debit"
	PaymentMethodTypeAUBECSDebit   PaymentMethodType = "au_becs_debit"
	PaymentMethodTypeACSSDebit     PaymentMethodType = "acss_debit"
)

// PaymentMethod is the neutral projection of a stored payment method. Exactly
// one of Card or BankDebit is set for the method types this framework models.
type PaymentMethod struct {
	Ref       string
	Type      PaymentMethodType
	Card      *Card
	BankDebit *BankDebit
}

// Card holds the non-sensitive details of a card payment method.
type Card struct {
	Brand          string
	Last4          string
	ExpMonth       int64
	ExpYear        int64
	Fingerprint    string
	CardholderName string
}

// BankDebit holds the non-sensitive details of a bank-debit payment method.
type BankDebit struct {
	Rail  PaymentMethodType
	Last4 string
}

// PaymentMethodDetach removes a stored payment method from its customer.
type PaymentMethodDetach struct {
	PaymentMethodRef string
	IdempotencyKey   string
}

// PaymentMethodUpdate edits the mutable fields of a stored card. Zero ExpMonth
// or ExpYear leaves the expiry unchanged; empty CardholderName leaves the name
// unchanged.
type PaymentMethodUpdate struct {
	PaymentMethodRef string
	ExpMonth         int64
	ExpYear          int64
	CardholderName   string
	IdempotencyKey   string
}
