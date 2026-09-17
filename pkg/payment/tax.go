package payment

// TaxBehavior says whether a price already includes tax or has it added on top.
type TaxBehavior string

const (
	TaxBehaviorUnspecified TaxBehavior = "unspecified"
	TaxBehaviorInclusive   TaxBehavior = "inclusive"
	TaxBehaviorExclusive   TaxBehavior = "exclusive"
)

// TaxAmount is one tax component the provider computed. Amount is the tax
// charged and TaxableAmount is the base it was computed on. Inclusive reports
// whether the tax was already part of the price. TaxRateRef is the provider's
// tax-rate identifier, when the provider attributes the tax to a defined rate.
type TaxAmount struct {
	Amount        Money
	TaxableAmount Money
	Inclusive     bool
	TaxRateRef    string
}
