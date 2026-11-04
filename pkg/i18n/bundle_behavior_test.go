package i18n

import (
	"errors"
	"testing"

	sdktemplate "github.com/nicksnyder/go-i18n/v2/i18n/template"
)

func TestCatalogCompilerRejectsMalformedTemplatesAndDuplicateMessages(t *testing.T) {
	base := Catalog{
		Domain: "billing", Revision: 1,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}},
		Keys:         []Key{{Definition: Definition{Key: "welcome", Source: Forms{Other: "Hello"}}, Translations: map[string]Translation{}}},
	}
	if _, err := compileCatalog("other", base); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("wrong catalog domain error=%v", err)
	}

	badSource := base
	badSource.Keys = append([]Key(nil), base.Keys...)
	badSource.Keys[0].Source.Other = `{{.Name`
	if _, err := compileCatalog("billing", badSource); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unsupported source template error=%v", err)
	}

	badTranslation := base
	badTranslation.DomainConfig = DomainConfig{DefaultLocale: "en", Locales: []string{"en", "fr"}}
	badTranslation.Keys = []Key{{
		Definition:   Definition{Key: "welcome", Source: Forms{Other: "Hello"}},
		Translations: map[string]Translation{"fr": {Forms: Forms{Other: `{{.Name`}}},
	}}
	if _, err := compileCatalog("billing", badTranslation); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unsupported translation template error=%v", err)
	}

	unsupportedLocale := base
	unsupportedLocale.DomainConfig = DomainConfig{DefaultLocale: "x-private", Locales: []string{"x-private"}}
	if _, err := compileCatalog("billing", unsupportedLocale); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("locale without a plural rule error=%v", err)
	}

	// Invalid templates are rejected by catalog validation before compilation;
	// the compiler also avoids caching their text as a static message if called
	// directly with a damaged cache payload.
	delimiterText := base
	delimiterText.Keys = append([]Key(nil), base.Keys...)
	delimiterText.Keys[0].Source.Other = `{{"{{"}}`
	compiled, err := compileCatalog("billing", delimiterText)
	if err != nil {
		t.Fatal(err)
	}
	if _, cached := compiled.static["welcome"]; cached {
		t.Fatal("template-looking message bypassed the validated parser through the static fast path")
	}
}

func TestCompiledParserOnlyServesPreviouslyValidatedTemplates(t *testing.T) {
	parser := &compiledParser{parsed: make(map[string]sdktemplate.ParsedTemplate)}
	if _, err := parser.Parse("unregistered", "", ""); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unregistered template error=%v", err)
	}
	if err := parser.add("Welcome {{.Name}}"); err != nil {
		t.Fatal(err)
	}
	if err := parser.add("Welcome {{.Name}}"); err != nil {
		t.Fatalf("duplicate template registration: %v", err)
	}
	parsed, err := parser.Parse("Welcome {{.Name}}", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parsed.Execute(map[string]any{"Name": "Mai"})
	if err != nil || got != "Welcome Mai" {
		t.Fatalf("validated template output=%q err=%v", got, err)
	}
	if err := parser.add(`{{.Name`); err == nil {
		t.Fatal("malformed template was cached")
	}
}
