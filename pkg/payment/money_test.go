package payment

import "testing"

func TestMoneyDecimalsByCurrency(t *testing.T) {
	t.Parallel()

	cases := map[string]int{
		"usd": 2, "USD": 2, "eur": 2,
		"vnd": 0, "jpy": 0, "krw": 0, " VND ": 0,
		"bhd": 3, "kwd": 3,
		"": 2, "zzz": 2,
	}

	for currency, want := range cases {
		if got := NewMoney(0, currency).Decimals(); got != want {
			t.Errorf("Decimals(%q) = %d, want %d", currency, got, want)
		}
	}
}

func TestMoneyMajor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		amount   int64
		currency string
		want     string
	}{
		{10000, "usd", "100.00"},
		{5, "usd", "0.05"},
		{-2599, "usd", "-25.99"},
		{10000, "vnd", "10000"}, // zero-decimal: already whole units
		{-200000, "vnd", "-200000"},
		{1000, "bhd", "1.000"}, // three-decimal
		{1, "bhd", "0.001"},
		{0, "usd", "0.00"},
	}

	for _, c := range cases {
		if got := NewMoney(c.amount, c.currency).Major(); got != c.want {
			t.Errorf("NewMoney(%d, %q).Major() = %q, want %q", c.amount, c.currency, got, c.want)
		}
	}
}

func TestMoneyString(t *testing.T) {
	t.Parallel()

	if got := NewMoney(10000, "usd").String(); got != "100.00 USD" {
		t.Errorf("String() = %q, want %q", got, "100.00 USD")
	}

	if got := NewMoney(200000, "vnd").String(); got != "200000 VND" {
		t.Errorf("String() = %q, want %q", got, "200000 VND")
	}
}

func TestMoneySameCurrencyAndZero(t *testing.T) {
	t.Parallel()

	if !NewMoney(1, "USD").SameCurrency(NewMoney(2, "usd")) {
		t.Error("SameCurrency must be case-insensitive")
	}

	if NewMoney(1, "usd").SameCurrency(NewMoney(1, "vnd")) {
		t.Error("different currencies must not be equal")
	}

	if !NewMoney(0, "usd").IsZero() || NewMoney(1, "usd").IsZero() {
		t.Error("IsZero is wrong")
	}
}
