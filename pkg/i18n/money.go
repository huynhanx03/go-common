package i18n

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

type moneyLocale struct {
	digits         [10]string
	decimal        string
	group          string
	negative       string
	primaryGroup   int
	secondaryGroup int
	minGroupDigits int
}

// FormatMoney renders exact minor units with locale-specific digits and grouping,
// followed by the ISO currency code. It never converts or rounds the amount.
func FormatMoney(localeName string, amount Money) (string, error) {
	tag, err := parseLocale(localeName)
	if err != nil {
		return "", ErrInvalidParams
	}
	unit, scale, err := currencyScale(amount.Currency)
	if err != nil {
		return "", err
	}
	return formatMoney(newMoneyLocale(tag), amount, unit, scale), nil
}

func currencyScale(code string) (currency.Unit, int, error) {
	if code == "" || len(code) != 3 {
		return currency.Unit{}, 0, ErrInvalidParams
	}
	unit, err := currency.ParseISO(strings.ToUpper(code))
	if err != nil || unit.String() == "XXX" {
		return currency.Unit{}, 0, ErrInvalidParams
	}
	scale, _ := currency.Standard.Rounding(unit)
	if scale < 0 || scale > 9 {
		return currency.Unit{}, 0, ErrInvalidParams
	}
	return unit, scale, nil
}

func newMoneyLocale(tag language.Tag) *moneyLocale {
	printer := message.NewPrinter(tag)
	format := &moneyLocale{minGroupDigits: 4}
	for digit := range 10 {
		format.digits[digit] = printer.Sprintf("%v", number.Decimal(digit, number.NoSeparator(), number.MaxFractionDigits(0)))
	}
	format.decimal = betweenDigits(printer.Sprintf("%v", number.Decimal(0, number.MinFractionDigits(1), number.MaxFractionDigits(1))))
	format.negative = prefixBeforeDigits(printer.Sprintf("%v", int64(-1)))
	format.primaryGroup, format.secondaryGroup, format.group = grouping(printer.Sprintf("%v", number.Decimal(uint64(123456789012345), number.MaxFractionDigits(0))))
	if format.primaryGroup > 0 {
		for digits := 2; digits <= 15; digits++ {
			value, _ := strconv.ParseUint("1"+strings.Repeat("0", digits-1), 10, 64)
			formatted := printer.Sprintf("%v", number.Decimal(value, number.MaxFractionDigits(0)))
			if strings.Contains(formatted, format.group) {
				format.minGroupDigits = digits
				break
			}
		}
	}
	return format
}

func grouping(sample string) (primary, secondary int, separator string) {
	groups := make([]int, 0, 8)
	var count int
	var between strings.Builder
	seenDigit := false
	for _, r := range sample {
		if unicode.IsDigit(r) {
			if !seenDigit {
				seenDigit = true
			}
			count++
			continue
		}
		if seenDigit && count > 0 {
			groups = append(groups, count)
			count = 0
			between.Reset()
		}
		if len(groups) > 0 {
			between.WriteRune(r)
		}
	}
	if count > 0 {
		groups = append(groups, count)
	}
	if len(groups) < 2 {
		return 0, 0, ""
	}
	separator = between.String()
	primary = groups[len(groups)-1]
	secondary = primary
	if len(groups) > 2 {
		secondary = groups[len(groups)-2]
	}
	return primary, secondary, separator
}

func betweenDigits(value string) string {
	runes := []rune(value)
	first, last := -1, -1
	for i, r := range runes {
		if unicode.IsDigit(r) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last <= first {
		return "."
	}
	return string(runes[first+1 : last])
}

func prefixBeforeDigits(value string) string {
	for i, r := range value {
		if unicode.IsDigit(r) {
			return value[:i]
		}
	}
	return "-"
}

func formatMoney(locale *moneyLocale, amount Money, unit currency.Unit, scale int) string {
	negative := amount.MinorUnits < 0
	var magnitude uint64
	if negative {
		magnitude = uint64(-(amount.MinorUnits + 1)) + 1
	} else {
		magnitude = uint64(amount.MinorUnits)
	}
	base := uint64(1)
	for range scale {
		base *= 10
	}
	major := groupInteger(strconv.FormatUint(magnitude/base, 10), locale)
	var numeric strings.Builder
	if negative {
		numeric.WriteString(locale.negative)
	}
	numeric.WriteString(localizeDigits(major, locale))
	if scale > 0 {
		fraction := strconv.FormatUint(magnitude%base, 10)
		if len(fraction) < scale {
			fraction = strings.Repeat("0", scale-len(fraction)) + fraction
		}
		numeric.WriteString(locale.decimal)
		numeric.WriteString(localizeDigits(fraction, locale))
	}
	return numeric.String() + " " + unit.String()
}

func groupInteger(value string, locale *moneyLocale) string {
	if locale.primaryGroup <= 0 || len(value) < locale.minGroupDigits {
		return value
	}
	groups := make([]string, 0, len(value)/locale.secondaryGroup+1)
	end, size := len(value), locale.primaryGroup
	for end > size {
		groups = append(groups, value[end-size:end])
		end -= size
		size = locale.secondaryGroup
	}
	groups = append(groups, value[:end])
	var out strings.Builder
	for i := len(groups) - 1; i >= 0; i-- {
		if i != len(groups)-1 {
			out.WriteString(locale.group)
		}
		out.WriteString(groups[i])
	}
	return out.String()
}

func localizeDigits(value string, locale *moneyLocale) string {
	var out strings.Builder
	for _, digit := range value {
		if digit >= '0' && digit <= '9' {
			out.WriteString(locale.digits[digit-'0'])
		} else {
			out.WriteRune(digit)
		}
	}
	return out.String()
}
