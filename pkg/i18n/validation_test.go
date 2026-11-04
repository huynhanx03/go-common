package i18n

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/feature/plural"
)

func TestRequestAndIdentifierValidation(t *testing.T) {
	if !isNil(nil) || !isNil((*int)(nil)) || !isNil((chan int)(nil)) || !isNil((func())(nil)) || !isNil(map[string]int(nil)) || !isNil([]int(nil)) {
		t.Fatal("typed nil dependencies must be rejected")
	}
	if isNil(0) || isNil("value") || isNil(make(chan int)) || isNil(func() {}) || isNil(map[string]int{}) || isNil([]int{}) {
		t.Fatal("non-nil dependencies must remain usable")
	}
	var nilContext context.Context
	if !errors.Is(contextOK(nilContext), ErrInvalidConfig) {
		t.Fatal("nil context must be rejected")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(contextOK(canceled), context.Canceled) || contextOK(context.Background()) != nil {
		t.Fatal("context cancellation must pass through")
	}

	for _, value := range []string{"", ".", "..", "bad key", "a/b", "naïve", strings.Repeat("x", MaxIdentifierBytes+1)} {
		if err := identifier(value); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("identifier %q accepted: %v", value, err)
		}
	}
	for _, value := range []string{"billing", "a.b-c:d_1", strings.Repeat("x", MaxIdentifierBytes)} {
		if err := identifier(value); err != nil {
			t.Errorf("identifier %q rejected: %v", value, err)
		}
	}
	if err := revisionOK(-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("negative revision: %v", err)
	}
	if err := revisionOK(math.MaxInt64); !errors.Is(err, ErrConflict) {
		t.Fatalf("exhausted revision: %v", err)
	}
	if err := revisionOK(math.MaxInt64 - 1); err != nil {
		t.Fatalf("last incrementable revision: %v", err)
	}
}

func TestLocaleAndDomainConfigurationValidation(t *testing.T) {
	for _, value := range []string{"", "en_US", "not a locale", strings.Repeat("a", maxLocaleTagBytes+1)} {
		if _, err := parseLocale(value); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("locale %q accepted: %v", value, err)
		}
	}
	if tag, err := parseLocale("vi-VN"); err != nil || tag.String() != "vi-VN" {
		t.Fatalf("canonical locale tag=%v err=%v", tag, err)
	}

	for name, config := range map[string]DomainConfig{
		"no locales":                  {DefaultLocale: "en"},
		"too many":                    {DefaultLocale: "en", Locales: make([]string, MaxLocales+1)},
		"invalid locale":              {DefaultLocale: "en_US", Locales: []string{"en_US"}},
		"locale without plural rules": {DefaultLocale: "x-private", Locales: []string{"x-private"}},
		"duplicate":                   {DefaultLocale: "en", Locales: []string{"en", "en"}},
		"missing default":             {DefaultLocale: "vi", Locales: []string{"en"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := configOK(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("invalid domain config accepted: %v", err)
			}
		})
	}
	if err := configOK(DomainConfig{DefaultLocale: "vi", Locales: []string{"en", "vi"}}); err != nil {
		t.Fatalf("valid domain config rejected: %v", err)
	}
}

func TestTemplateValidationAllowsOnlySimpleNamedParameters(t *testing.T) {
	got, err := fields("Hello {{.Name}} {{.Amount}} {{.Name}}")
	if err != nil || !reflect.DeepEqual(got, []string{"Amount", "Name"}) {
		t.Fatalf("template parameters=%v err=%v", got, err)
	}
	if got, err := fields("plain text"); err != nil || len(got) != 0 {
		t.Fatalf("static template params=%v err=%v", got, err)
	}

	tooManyParams := make([]string, MaxParams+1)
	for i := range tooManyParams {
		tooManyParams[i] = fmt.Sprintf("{{.Param%d}}", i)
	}
	for name, input := range map[string]string{
		"nul":                "bad\x00template",
		"oversized":          strings.Repeat("x", MaxTemplateBytes+1),
		"invalid utf8":       string([]byte{0xff}),
		"syntax":             "{{.Name",
		"function call":      `{{printf "%s" .Name}}`,
		"pipeline":           `{{.Name | printf "%s"}}`,
		"variable":           `{{$value := .Name}}`,
		"nested action":      `{{if .Name}}yes{{end}}`,
		"nested field":       "{{.User.Name}}",
		"non-ascii name":     "{{.Náme}}",
		"too many fields":    strings.Join(tooManyParams, " "),
		"multiple templates": `{{define "named"}}named{{end}}root`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fields(input)
			want := ErrInvalidDefinition
			if name == "oversized" || name == "invalid utf8" {
				want = ErrLimit
			}
			if !errors.Is(err, want) {
				t.Fatalf("unsafe template returned %v, want %v", err, want)
			}
		})
	}
}

func TestPluralFormsAreCheckedAgainstLocaleRules(t *testing.T) {
	key := Key{Definition: Definition{Key: "items", Plural: true, Source: Forms{Other: "{{.Count}} items"}}}
	if err := translationReady(key, "en", Forms{One: "{{.Count}} item", Other: "{{.Count}} items"}); err != nil {
		t.Fatalf("English plural forms rejected: %v", err)
	}
	if err := translationReady(key, "ar", Forms{Other: "{{.Count}} items"}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete Arabic plural forms accepted: %v", err)
	}
	if err := translationReady(key, "not a locale", Forms{Other: "items"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid plural locale accepted: %v", err)
	}
	if err := translationReadyForForms(Key{Definition: Definition{Source: Forms{Other: "{{.Count}} items"}}}, Forms{One: "{{.Count}} item", Other: "{{.Count}} items"}, nil); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("non-plural key accepted plural forms: %v", err)
	}
	if err := translationReadyForForms(key, Forms{Other: "{{.Wrong}} items"}, []int{int(plural.One)}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("placeholder mismatch accepted: %v", err)
	}
	if err := translationReadyForForms(key, Forms{Other: "{{.Count}} items"}, []int{int(plural.One)}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("required locale form missing: %v", err)
	}
	if err := translationReadyForForms(Key{Definition: Definition{Plural: true, Source: Forms{Other: "{{.Name"}}}, Forms{Other: "{{.Name}}"}, nil); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid source template in translation readiness=%v", err)
	}
	if err := translationReadyForForms(key, Forms{Other: "{{.Name"}, []int{int(plural.One)}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid translated template in readiness=%v", err)
	}
}

func TestFormCategoriesMustUseTheSameTemplateParameters(t *testing.T) {
	if _, err := formsOK(Forms{One: "Hello {{.Name}}", Other: "Hello {{.User}}"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("different placeholder contract accepted: %v", err)
	}
	if got, err := formsOK(Forms{One: "One {{.Name}}", Other: "Other {{.Name}}"}); err != nil || !reflect.DeepEqual(got, []string{"Name"}) {
		t.Fatalf("consistent plural placeholder contract=%v err=%v", got, err)
	}
}

func TestDefinitionValidationProtectsPublishedMessageContracts(t *testing.T) {
	valid := Definition{Key: "welcome", Description: "Greeting", Source: Forms{Other: "Hello {{.Name}}"}}
	if err := definitionOK(valid); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	invalid := []Definition{
		{Key: "bad key", Source: Forms{Other: "x"}},
		{Key: "welcome", Description: strings.Repeat("x", MaxDescriptionBytes+1), Source: Forms{Other: "x"}},
		{Key: "welcome", Description: string([]byte{0xff}), Source: Forms{Other: "x"}},
		{Key: "welcome", Description: "bad\x00description", Source: Forms{Other: "x"}},
		{Key: "welcome"},
		{Key: "welcome", Source: Forms{Other: "{{.PluralCount}}"}},
	}
	for _, definition := range invalid {
		if err := definitionOK(definition); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("invalid definition accepted: %+v err=%v", definition, err)
		}
	}
	if !utf8.ValidString(valid.Source.Other) {
		t.Fatal("test fixture is not valid UTF-8")
	}
}

func TestNormalizeCatalogOwnsCanonicalCompleteSnapshots(t *testing.T) {
	catalog := Catalog{
		Domain: "billing", Revision: 3,
		DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}},
		Keys: []Key{
			{Definition: Definition{Key: "zeta", Source: Forms{Other: "Xin chào"}}, Translations: map[string]Translation{"en": {Forms: Forms{Other: "Hello"}, Reviewed: true}}},
			{Definition: Definition{Key: "alpha", Source: Forms{Other: "Tạm biệt"}}, Translations: map[string]Translation{"en": {Forms: Forms{Other: "Goodbye"}, Reviewed: true}}},
		},
	}
	owned, err := NormalizeCatalog(catalog, true)
	if err != nil {
		t.Fatal(err)
	}
	if owned.Keys[0].Key != "alpha" || owned.Locales[0] != "en" {
		t.Fatalf("catalog was not canonicalized: locales=%v keys=%v", owned.Locales, []string{owned.Keys[0].Key, owned.Keys[1].Key})
	}
	owned.Keys[0].Translations["en"] = Translation{Forms: Forms{Other: "mutated"}}
	if catalog.Keys[1].Translations["en"].Forms.Other != "Goodbye" {
		t.Fatal("normalized snapshot still aliases caller-owned translation maps")
	}
	if _, err := NormalizeCatalog(catalog, false); err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}

	incomplete := catalog
	incomplete.Keys = append([]Key(nil), catalog.Keys...)
	incomplete.Keys[0].Translations = map[string]Translation{}
	if _, err := NormalizeCatalog(incomplete, true); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("untranslated publication accepted: %v", err)
	}
	incomplete = catalog
	incomplete.Keys = append([]Key(nil), catalog.Keys...)
	incomplete.Keys[0].Translations = map[string]Translation{"en": {Forms: Forms{Other: "Hello"}}}
	if _, err := NormalizeCatalog(incomplete, true); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("unreviewed publication accepted: %v", err)
	}
}

func TestNormalizeCatalogRejectsInvalidRowsAndPayloadLimits(t *testing.T) {
	validCatalog := func() Catalog {
		return Catalog{
			Domain: "billing", Revision: 1,
			DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en", "vi"}},
			Keys:         []Key{{Definition: Definition{Key: "welcome", Source: Forms{Other: "Hello"}}, Translations: map[string]Translation{"vi": {Forms: Forms{Other: "Xin chào"}}}}},
		}
	}
	cases := map[string]struct {
		catalog Catalog
		want    error
	}{
		"invalid domain":   {func() Catalog { c := validCatalog(); c.Domain = "bad domain"; return c }(), ErrInvalidDefinition},
		"missing revision": {func() Catalog { c := validCatalog(); c.Revision = 0; return c }(), ErrInvalidDefinition},
		"invalid config":   {func() Catalog { c := validCatalog(); c.Locales = []string{"en", "en"}; return c }(), ErrInvalidConfig},
		"invalid key":      {func() Catalog { c := validCatalog(); c.Keys[0].Key = "bad key"; return c }(), ErrInvalidDefinition},
		"duplicate key":    {func() Catalog { c := validCatalog(); c.Keys = append(c.Keys, c.Keys[0]); return c }(), ErrInvalidDefinition},
		"default translation": {func() Catalog {
			c := validCatalog()
			c.Keys[0].Translations["en"] = Translation{Forms: Forms{Other: "Wrong"}}
			return c
		}(), ErrInvalidDefinition},
		"unsupported translation": {func() Catalog {
			c := validCatalog()
			c.Keys[0].Translations["fr"] = Translation{Forms: Forms{Other: "Bonjour"}}
			return c
		}(), ErrInvalidDefinition},
		"too many translation rows": {func() Catalog {
			c := validCatalog()
			for i := 0; i <= MaxLocales; i++ {
				c.Keys[0].Translations[fmt.Sprintf("x-%d", i)] = Translation{}
			}
			return c
		}(), ErrLimit},
		"too many keys": {func() Catalog { c := validCatalog(); c.Keys = make([]Key, MaxKeys+1); return c }(), ErrLimit},
		"oversized raw payload": {func() Catalog {
			c := validCatalog()
			c.Keys = make([]Key, 4200)
			for i := range c.Keys {
				c.Keys[i] = Key{Definition: Definition{Key: fmt.Sprintf("k%d", i), Description: strings.Repeat("d", MaxDescriptionBytes), Source: Forms{Other: "x"}}}
			}
			return c
		}(), ErrLimit},
		"oversized encoded payload": {func() Catalog {
			c := validCatalog()
			c.Keys = make([]Key, 2500)
			for i := range c.Keys {
				c.Keys[i] = Key{Definition: Definition{Key: fmt.Sprintf("k%d", i), Description: strings.Repeat("\"", MaxDescriptionBytes), Source: Forms{Other: "x"}}}
			}
			return c
		}(), ErrLimit},
		"invalid source plural shape": {func() Catalog {
			c := validCatalog()
			c.Keys[0].Source = Forms{One: "Hello", Other: "Hello"}
			return c
		}(), ErrInvalidDefinition},
		"invalid translation template": {func() Catalog {
			c := validCatalog()
			c.Keys[0].Translations["vi"] = Translation{Forms: Forms{Other: `{{.Name`}}
			return c
		}(), ErrInvalidDefinition},
		"reviewed incomplete translation": {func() Catalog {
			c := validCatalog()
			c.Keys[0].Translations["vi"] = Translation{Reviewed: true}
			return c
		}(), ErrIncomplete},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeCatalog(test.catalog, false); !errors.Is(err, test.want) {
				t.Fatalf("catalog validation error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateStateKeepsDraftAndPublicationConsistent(t *testing.T) {
	draft := Catalog{Domain: "billing", Revision: 2, DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}, Keys: []Key{}}
	state, err := ValidateState("billing", State{Draft: draft})
	if err != nil || state.Draft.Revision != draft.Revision {
		t.Fatalf("valid draft state=%+v err=%v", state, err)
	}
	published := draft
	published.Revision = 1
	state, err = ValidateState("billing", State{Draft: draft, Published: &published})
	if err != nil || state.Published == nil || state.Published.Revision != 1 {
		t.Fatalf("valid published state=%+v err=%v", state, err)
	}
	if _, err := NormalizeCatalog(Catalog{Domain: "billing", Revision: 1, DomainConfig: draft.DomainConfig}, false); err != nil {
		t.Fatalf("empty catalog without a key slice rejected: %v", err)
	}
	invalid := []State{
		{Draft: Catalog{Domain: "other", Revision: 1, DomainConfig: draft.DomainConfig}},
		{Draft: draft, Published: &Catalog{Domain: "other", Revision: 1, DomainConfig: draft.DomainConfig}},
		{Draft: draft, Published: &Catalog{Domain: "billing", Revision: 3, DomainConfig: draft.DomainConfig}},
		{Draft: draft, Published: &Catalog{Domain: "billing", Revision: 1, DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en", "en"}}}},
	}
	for i, candidate := range invalid {
		_, err := ValidateState("billing", candidate)
		want := ErrInvalidDefinition
		if i == len(invalid)-1 {
			want = ErrInvalidConfig
		}
		if !errors.Is(err, want) {
			t.Errorf("inconsistent state error=%v, want %v", err, want)
		}
	}

	large := draft
	large.Keys = make([]Key, 2050)
	for i := range large.Keys {
		large.Keys[i] = Key{Definition: Definition{Key: fmt.Sprintf("k%d", i), Description: strings.Repeat("d", MaxDescriptionBytes), Source: Forms{Other: "x"}}}
	}
	if _, err := ValidateState("billing", State{Draft: large, Published: &large}); !errors.Is(err, ErrLimit) {
		t.Fatalf("combined state exceeded its persistence limit without error: %v", err)
	}
}
