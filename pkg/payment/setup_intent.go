package payment

// SetupIntentStatus is the neutral status of an off-session card setup.
type SetupIntentStatus string

const (
	SetupIntentStatusRequiresPaymentMethod SetupIntentStatus = "requires_payment_method"
	SetupIntentStatusRequiresConfirmation  SetupIntentStatus = "requires_confirmation"
	SetupIntentStatusRequiresAction        SetupIntentStatus = "requires_action"
	SetupIntentStatusProcessing            SetupIntentStatus = "processing"
	SetupIntentStatusCanceled              SetupIntentStatus = "canceled"
	SetupIntentStatusSucceeded             SetupIntentStatus = "succeeded"
)

// SetupIntent is the neutral projection of an off-session setup used to store a
// payment method for later charges.
type SetupIntent struct {
	Ref                string
	CustomerRef        string
	CustomerAccountRef string
	PaymentMethodRef   string
	Status             SetupIntentStatus
	ClientSecret       string
	Metadata           map[string]string
}

// Succeeded reports whether the setup completed and the method is ready to bill.
func (s *SetupIntent) Succeeded() bool {
	return s != nil && s.Status == SetupIntentStatusSucceeded
}

// NewSetupIntent starts an off-session setup for a customer account.
type NewSetupIntent struct {
	CustomerRef        string
	CustomerAccountRef string
	Metadata           map[string]string
	IdempotencyKey     string
}
