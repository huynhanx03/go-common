package i18n

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/nicksnyder/go-i18n/v2/i18n"
	sdktemplate "github.com/nicksnyder/go-i18n/v2/i18n/template"
	"golang.org/x/text/language"
)

type failedTemplate struct{}

func (failedTemplate) Execute(any) (string, error) {
	return "", errors.New("template execution failed")
}

func TestRenderEnforcesCatalogAndTemplateContracts(t *testing.T) {
	catalog := Catalog{
		Domain: "billing", Revision: 7,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
		Keys: []Key{
			{Definition: Definition{Key: "static", Source: Forms{Other: "ready"}}, Translations: map[string]Translation{}},
			{Definition: Definition{Key: "greeting", Source: Forms{Other: "Hello {{.Name}}"}}, Translations: map[string]Translation{}},
			{Definition: Definition{Key: "items", Plural: true, Source: Forms{Other: "{{.Count}} items"}}, Translations: map[string]Translation{}},
			{Definition: Definition{Key: "plural.count", Plural: true, Source: Forms{Other: "{{.PluralCount}} items"}}, Translations: map[string]Translation{}},
		},
	}
	snapshot, err := compileCatalog("billing", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := render(snapshot, "en", "static", Options{}); err != nil || got.Text != "ready" || got.Revision != 7 {
		t.Fatalf("static message=%+v err=%v", got, err)
	}
	if _, err := render(snapshot, "en", "unknown", Options{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key error=%v", err)
	}
	if _, err := render(snapshot, "en", "static", Options{PluralCount: 1}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("plural count on non-plural message error=%v", err)
	}
	if _, err := render(snapshot, "en", "greeting", Options{}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("missing template parameter error=%v", err)
	}
	if got, err := render(snapshot, "en", "greeting", Options{Params: map[string]any{"Name": "Linh"}}); err != nil || got.Text != "Hello Linh" {
		t.Fatalf("parameterized message=%+v err=%v", got, err)
	}
	if _, err := render(snapshot, "en", "items", Options{Params: map[string]any{"Count": 2}, PluralCount: float64(2)}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("unsupported plural count error=%v", err)
	}
	if got, err := render(snapshot, "en", "plural.count", Options{PluralCount: int(2)}); err != nil || got.Text != "2 items" {
		t.Fatalf("plural count template=%+v err=%v", got, err)
	}

	// A catalog can become stale between manifest and locale reads. A missing
	// compiled message must stay a cache miss instead of leaking an SDK error.
	missing := &compiledCatalog{
		revision: 9,
		params:   map[string][]string{"gone": {}},
		localizers: map[string]*sdk.Localizer{
			"en": sdk.NewLocalizer(sdk.NewBundle(language.English), "en"),
		},
		parser: &compiledParser{parsed: map[string]sdktemplate.ParsedTemplate{}},
	}
	if got, err := render(missing, "en", "gone", Options{}); !errors.Is(err, ErrNotFound) || got.Text != "gone" {
		t.Fatalf("stale compiled message=%+v err=%v", got, err)
	}

	broken := &compiledCatalog{
		revision: 9, params: map[string][]string{"broken": {"Name"}},
		localizers: map[string]*sdk.Localizer{},
		parser:     &compiledParser{parsed: map[string]sdktemplate.ParsedTemplate{"{{.Name}}": failedTemplate{}}},
	}
	bundle := sdk.NewBundle(language.English)
	if err := bundle.AddMessages(language.English, &sdk.Message{ID: "broken", Other: "{{.Name}}"}); err != nil {
		t.Fatal(err)
	}
	broken.localizers["en"] = sdk.NewLocalizer(bundle, "en")
	if _, err := render(broken, "en", "broken", Options{Params: map[string]any{"Name": "x"}}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("template execution failure error=%v", err)
	}
}

func TestTemplateParametersAcceptSafeScalarAndMoneyValues(t *testing.T) {
	snapshot := &compiledCatalog{moneyFormats: map[string]*moneyLocale{"en": newMoneyLocale(language.English)}}
	values := []any{
		"text", true, int(-1), int8(-2), int16(-3), int32(-4), int64(-5),
		uint(1), uint8(2), uint16(3), uint32(4), uint64(5), float32(1.25), float64(2.5),
	}
	for _, value := range values {
		if _, err := copyParam(snapshot, "en", value); err != nil {
			t.Errorf("supported scalar %T rejected: %v", value, err)
		}
	}
	if got, err := copyParam(snapshot, "en", Money{MinorUnits: 1234, Currency: "USD"}); err != nil || got != "12.34 USD" {
		t.Fatalf("money parameter=%v err=%v", got, err)
	}

	var nilPointer *int
	var nilMap map[string]string
	for _, value := range []any{
		nil, nilPointer, nilMap, struct{ Name string }{"x"},
		float32(float32(math.Inf(1))), math.NaN(), math.Inf(-1),
		string([]byte{0xff}), strings.Repeat("x", MaxParamBytes+1),
		Money{MinorUnits: 1, Currency: ""},
	} {
		if _, err := copyParam(snapshot, "en", value); err == nil {
			t.Errorf("unsafe parameter %T was accepted", value)
		}
	}
}

func TestCopyParamsRejectsReservedNamesAndResourceAbuse(t *testing.T) {
	snapshot := &compiledCatalog{moneyFormats: map[string]*moneyLocale{"en": newMoneyLocale(language.English)}}
	for _, params := range []map[string]any{
		{"bad name": "x"},
		{"PluralCount": 2},
		{"Name": strings.Repeat("x", MaxParamBytes+1)},
	} {
		if _, err := copyParams(snapshot, "en", params); err == nil {
			t.Errorf("invalid params accepted: %#v", params)
		}
	}
	tooMany := make(map[string]any, MaxParams+1)
	for i := 0; i <= MaxParams; i++ {
		tooMany["p"+string(rune('a'+i))] = i
	}
	if _, err := copyParams(snapshot, "en", tooMany); !errors.Is(err, ErrLimit) {
		t.Fatalf("parameter count limit error=%v", err)
	}
	valid, err := copyParams(snapshot, "en", map[string]any{"Count": int64(3)})
	if err != nil || !reflect.DeepEqual(valid, map[string]any{"Count": int64(3)}) {
		t.Fatalf("valid parameter copy=%#v err=%v", valid, err)
	}
}

func TestPluralCountAcceptsOnlySupportedRepresentations(t *testing.T) {
	for _, value := range []any{int(1), int8(1), int16(1), int32(1), int64(1), "1.5", "0"} {
		if got, err := pluralValue(value); err != nil || got != value {
			t.Errorf("valid plural count %T(%v) => %v, %v", value, value, got, err)
		}
	}
	for _, value := range []any{nil, uint(1), float64(1), true, "", strings.Repeat("1", MaxParamBytes+1)} {
		if _, err := pluralValue(value); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("invalid plural count %T(%v) error=%v", value, value, err)
		}
	}
}
