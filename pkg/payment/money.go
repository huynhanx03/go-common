package payment

import (
	"strconv"
	"strings"
)

// Money is a currency-aware amount. Amount is always in the currency's minor
// unit — cents for USD, but whole units for zero-decimal currencies such as VND
// and JPY, where 10000 means ₫10,000, not ₫100.00. Currency is an ISO 4217
// code; it is compared and formatted case-insensitively and stored as given.
//
// Money is a small value type: pass and return it by value.
type Money struct {
	Amount   int64
	Currency string
}

// NewMoney builds a Money in the currency's minor unit.
func NewMoney(amount int64, currency string) Money {
	return Money{Amount: amount, Currency: currency}
}

// zeroDecimalCurrencies have no minor unit: the amount is already whole units.
var zeroDecimalCurrencies = map[string]struct{}{
	"bif": {}, "clp": {}, "djf": {}, "gnf": {}, "jpy": {}, "kmf": {},
	"krw": {}, "mga": {}, "pyg": {}, "rwf": {}, "ugx": {}, "vnd": {},
	"vuv": {}, "xaf": {}, "xof": {}, "xpf": {},
}

// threeDecimalCurrencies use a thousandth as their minor unit.
var threeDecimalCurrencies = map[string]struct{}{
	"bhd": {}, "jod": {}, "kwd": {}, "omr": {}, "tnd": {},
}

// Decimals returns how many fractional digits the currency's minor unit implies
// (0 for VND/JPY, 3 for BHD/KWD, 2 otherwise). An empty currency defaults to 2.
func (m Money) Decimals() int {
	c := strings.ToLower(strings.TrimSpace(m.Currency))
	if _, ok := zeroDecimalCurrencies[c]; ok {
		return 0
	}
	if _, ok := threeDecimalCurrencies[c]; ok {
		return 3
	}

	return 2
}

// IsZero reports whether the amount is zero (regardless of currency).
func (m Money) IsZero() bool { return m.Amount == 0 }

// SameCurrency reports whether two amounts share a currency, case-insensitively.
func (m Money) SameCurrency(other Money) bool {
	return strings.EqualFold(m.Currency, other.Currency)
}

// Major renders the amount in major units as an exact decimal string, e.g.
// "100.00" for 10000 USD or "10000" for 10000 VND. It never uses floating point,
// so no rounding is introduced.
func (m Money) Major() string {
	decimals := m.Decimals()

	neg := m.Amount < 0
	abs := m.Amount
	if neg {
		abs = -abs
	}

	digits := strconv.FormatInt(abs, 10)

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}

	if decimals == 0 {
		b.WriteString(digits)
		return b.String()
	}

	// Left-pad so there is at least one integer digit before the point.
	for len(digits) <= decimals {
		digits = "0" + digits
	}

	split := len(digits) - decimals
	b.WriteString(digits[:split])
	b.WriteByte('.')
	b.WriteString(digits[split:])

	return b.String()
}

// String renders the amount for humans as "<major> <CURRENCY>", e.g.
// "100.00 USD" or "10000 VND".
func (m Money) String() string {
	return m.Major() + " " + strings.ToUpper(strings.TrimSpace(m.Currency))
}
