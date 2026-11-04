package i18n

import (
	"strings"

	sdk "github.com/nicksnyder/go-i18n/v2/i18n"
	sdktemplate "github.com/nicksnyder/go-i18n/v2/i18n/template"
	"golang.org/x/text/language"
)

type compiledCatalog struct {
	revision     int64
	localizers   map[string]*sdk.Localizer
	parser       *compiledParser
	params       map[string][]string
	plural       map[string]bool
	static       map[string]map[string]string
	moneyFormats map[string]*moneyLocale
}

// compileCatalog compiles a projection already checked by validateProjection.
func compileCatalog(domain string, catalog Catalog) (*compiledCatalog, error) {
	if catalog.Domain != domain {
		return nil, ErrInvalidDefinition
	}
	defaultTag, _ := parseLocale(catalog.DefaultLocale)
	bundle := sdk.NewBundle(defaultTag)
	strictParser := &compiledParser{parsed: make(map[string]sdktemplate.ParsedTemplate)}
	params := make(map[string][]string, len(catalog.Keys))
	plural := make(map[string]bool, len(catalog.Keys))
	static := make(map[string]map[string]string)
	for _, key := range catalog.Keys {
		params[key.Key], _ = formsOK(key.Source)
		plural[key.Key] = key.Plural
		for _, form := range formValues(key.Source) {
			if form != "" {
				if err := strictParser.add(form); err != nil {
					return nil, ErrInvalidDefinition
				}
			}
		}
		for _, translation := range key.Translations {
			for _, form := range formValues(translation.Forms) {
				if form != "" {
					if err := strictParser.add(form); err != nil {
						return nil, ErrInvalidDefinition
					}
				}
			}
		}
		if !key.Plural && len(params[key.Key]) == 0 {
			localized := make(map[string]string, len(catalog.Locales))
			for _, localeName := range catalog.Locales {
				forms := key.Source
				if localeName != catalog.DefaultLocale {
					forms = key.Translations[localeName].Forms
				}
				if strings.Contains(forms.Other, "{{") {
					clear(localized)
					break
				}
				localized[localeName] = forms.Other
			}
			if len(localized) == len(catalog.Locales) {
				static[key.Key] = localized
			}
		}
	}
	tags := make([]language.Tag, 0, len(catalog.Locales))
	localeNames := make([]string, 0, len(catalog.Locales))
	byLocale := make(map[string]language.Tag, len(catalog.Locales))
	for _, name := range catalog.Locales {
		tag, _ := parseLocale(name)
		tags = append(tags, tag)
		localeNames = append(localeNames, name)
		byLocale[name] = tag
	}
	for _, localeName := range localeNames {
		messages := make([]*sdk.Message, 0, len(catalog.Keys))
		for _, key := range catalog.Keys {
			forms := key.Source
			if localeName != catalog.DefaultLocale {
				forms = key.Translations[localeName].Forms
			}
			messages = append(messages, sdkMessage(key, forms))
		}
		if len(messages) > 0 {
			if err := bundle.AddMessages(byLocale[localeName], messages...); err != nil {
				return nil, ErrInvalidDefinition
			}
		}
	}
	localizers := make(map[string]*sdk.Localizer, len(localeNames))
	moneyFormats := make(map[string]*moneyLocale, len(localeNames))
	for _, name := range localeNames {
		localizers[name] = sdk.NewLocalizer(bundle, name, catalog.DefaultLocale)
		moneyFormats[name] = newMoneyLocale(byLocale[name])
	}
	return &compiledCatalog{
		revision: catalog.Revision, localizers: localizers,
		parser: strictParser,
		params: params, plural: plural, static: static, moneyFormats: moneyFormats,
	}, nil
}

func sdkMessage(key Key, forms Forms) *sdk.Message {
	return &sdk.Message{ID: key.Key, Description: key.Description, Zero: forms.Zero, One: forms.One, Two: forms.Two, Few: forms.Few, Many: forms.Many, Other: forms.Other}
}

type compiledParser struct {
	parsed map[string]sdktemplate.ParsedTemplate
}

func (p *compiledParser) Cacheable() bool { return true }
func (p *compiledParser) Parse(src, left, right string) (sdktemplate.ParsedTemplate, error) {
	parsed, ok := p.parsed[src]
	if !ok {
		return nil, ErrInvalidDefinition
	}
	return parsed, nil
}
func (p *compiledParser) add(src string) error {
	if _, exists := p.parsed[src]; exists {
		return nil
	}
	parsed, err := (&sdktemplate.TextParser{Option: strictTemplateOption}).Parse(src, "", "")
	if err == nil {
		p.parsed[src] = parsed
	}
	return err
}
