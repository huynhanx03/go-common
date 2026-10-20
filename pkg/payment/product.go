package payment

import "strings"

// Product is the neutral projection of a sellable product in the provider's
// catalog.
type Product struct {
	Ref               string
	Name              string
	Description       string
	Active            bool
	Images            []string
	MarketingFeatures []string
	Metadata          map[string]string
}

// PriceType distinguishes a one-off price from a recurring one.
type PriceType string

const (
	PriceTypeOneTime   PriceType = "one_time"
	PriceTypeRecurring PriceType = "recurring"
)

// RecurringInterval is the billing cadence unit of a recurring price.
type RecurringInterval string

const (
	RecurringIntervalDay   RecurringInterval = "day"
	RecurringIntervalWeek  RecurringInterval = "week"
	RecurringIntervalMonth RecurringInterval = "month"
	RecurringIntervalYear  RecurringInterval = "year"
)

// Price is the neutral projection of a product's price.
//
// UnitAmount is the price in its base currency. CurrencyOptions holds the same
// price expressed in other currencies, keyed by lowercase ISO 4217 code — this
// is how one product carries region-specific pricing (e.g. USD 10.00 for the US
// and VND 200000 for Vietnam) instead of a hard currency conversion. Use
// AmountFor to resolve the amount for a given region's currency.
type Price struct {
	Ref             string
	ProductRef      string
	Active          bool
	Type            PriceType
	UnitAmount      Money
	TaxBehavior     TaxBehavior
	CurrencyOptions map[string]Money
	Metadata        map[string]string
	Recurring       *PriceRecurrence
}

// AmountFor resolves the price in the given currency: the base UnitAmount when
// it matches, otherwise a CurrencyOptions entry. ok is false when the price is
// not offered in that currency.
func (p *Price) AmountFor(currency string) (Money, bool) {
	if p == nil {
		return Money{}, false
	}

	if p.UnitAmount.SameCurrency(NewMoney(0, currency)) {
		return p.UnitAmount, true
	}

	if p.CurrencyOptions != nil {
		if m, ok := p.CurrencyOptions[strings.ToLower(strings.TrimSpace(currency))]; ok {
			return m, true
		}
	}

	return Money{}, false
}

// PriceRecurrence describes how a recurring price repeats.
type PriceRecurrence struct {
	Interval        RecurringInterval
	IntervalCount   int64
	TrialPeriodDays int64
}
