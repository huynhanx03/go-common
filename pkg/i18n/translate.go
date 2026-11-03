package i18n

import (
	"errors"
	"math"
	"unicode/utf8"

	sdk "github.com/nicksnyder/go-i18n/v2/i18n"
)

func render(snapshot *compiledCatalog, localeName, key string, options Options) (Result, error) {
	fields, exists := snapshot.params[key]
	if !exists {
		return Result{Text: key, Locale: localeName, Revision: snapshot.revision}, ErrNotFound
	}
	if !snapshot.plural[key] && options.PluralCount != nil {
		return Result{}, ErrInvalidParams
	}
	if localized, ok := snapshot.static[key]; ok && len(options.Params) == 0 && options.PluralCount == nil {
		return Result{Text: localized[localeName], Locale: localeName, Revision: snapshot.revision}, nil
	}
	values, err := copyParams(snapshot, localeName, options.Params)
	if err != nil {
		return Result{}, err
	}
	for _, field := range fields {
		if field == "PluralCount" && options.PluralCount != nil {
			continue
		}
		if _, ok := values[field]; !ok {
			return Result{}, ErrInvalidParams
		}
	}
	var count any
	if snapshot.plural[key] {
		count, err = pluralValue(options.PluralCount)
		if err != nil {
			return Result{}, ErrInvalidParams
		}
		values["PluralCount"] = count
	}
	text, err := snapshot.localizers[localeName].Localize(&sdk.LocalizeConfig{
		MessageID: key, TemplateData: values, PluralCount: count,
		TemplateParser: snapshot.parser,
	})
	if err != nil {
		var missing *sdk.MessageNotFoundErr
		if errors.As(err, &missing) {
			return Result{Text: key, Locale: localeName, Revision: snapshot.revision}, ErrNotFound
		}
		return Result{}, ErrInvalidParams
	}
	return Result{Text: text, Locale: localeName, Revision: snapshot.revision}, nil
}

func copyParams(snapshot *compiledCatalog, localeName string, params map[string]any) (map[string]any, error) {
	if len(params) > MaxParams {
		return nil, ErrLimit
	}
	out := make(map[string]any, len(params)+1)
	for name, value := range params {
		if identifier(name) != nil || name == "PluralCount" {
			return nil, ErrInvalidParams
		}
		converted, err := copyParam(snapshot, localeName, value)
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	return out, nil
}

func copyParam(snapshot *compiledCatalog, localeName string, value any) (any, error) {
	switch value := value.(type) {
	case Money:
		unit, scale, err := currencyScale(value.Currency)
		if err != nil {
			return nil, err
		}
		return formatMoney(snapshot.moneyFormats[localeName], value, unit, scale), nil
	case string:
		if len(value) > MaxParamBytes || !utf8.ValidString(value) {
			return nil, ErrLimit
		}
		return value, nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value, nil
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, ErrInvalidParams
		}
		return value, nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrInvalidParams
		}
		return value, nil
	default:
		if value == nil || isNil(value) {
			return nil, ErrInvalidParams
		}
		return nil, ErrInvalidParams
	}
}

func pluralValue(value any) (any, error) {
	switch value := value.(type) {
	case int:
		return value, nil
	case int8:
		return value, nil
	case int16:
		return value, nil
	case int32:
		return value, nil
	case int64:
		return value, nil
	case string:
		if value != "" && len(value) <= MaxParamBytes {
			return value, nil
		}
	}
	return nil, ErrInvalidParams
}
