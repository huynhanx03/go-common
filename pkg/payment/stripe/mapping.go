package stripe

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// moneyOf builds a neutral Money from a Stripe minor-unit amount and currency.
func moneyOf(amount int64, currency stripesdk.Currency) payment.Money {
	return payment.NewMoney(amount, string(currency))
}

// unixPtrOrNil converts a Unix-seconds timestamp to a UTC *time.Time, mapping
// the zero timestamp (Stripe's "unset") to nil.
func unixPtrOrNil(sec int64) *time.Time {
	if sec == 0 {
		return nil
	}

	t := time.Unix(sec, 0).UTC()

	return &t
}
