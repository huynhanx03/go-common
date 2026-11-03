package i18n

import (
	"errors"
	"testing"
)

func TestMoneyFormattingRejectsInvalidLocaleAndCurrencyCodes(t *testing.T) {
	for _, test := range []struct {
		locale   string
		currency string
	}{
		{locale: "en_US", currency: "USD"},
		{locale: "en", currency: ""},
		{locale: "en", currency: "XXX"},
	} {
		if _, err := FormatMoney(test.locale, Money{MinorUnits: 100, Currency: test.currency}); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("locale=%q currency=%q error=%v", test.locale, test.currency, err)
		}
	}
}

func TestMoneyLocaleParsingHasSafeFallbacksWithoutDigitGrouping(t *testing.T) {
	if primary, secondary, separator := grouping("12345"); primary != 0 || secondary != 0 || separator != "" {
		t.Fatalf("ungrouped numeral metadata=(%d,%d,%q)", primary, secondary, separator)
	}
	if got := betweenDigits("zero"); got != "." {
		t.Fatalf("decimal separator fallback=%q", got)
	}
	if got := prefixBeforeDigits("minus"); got != "-" {
		t.Fatalf("negative prefix fallback=%q", got)
	}
	if got, err := FormatMoney("ja", Money{MinorUnits: 1234, Currency: "JPY"}); err != nil || got == "" {
		t.Fatalf("Japanese amount=%q err=%v", got, err)
	}
}
