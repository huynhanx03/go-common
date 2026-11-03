package i18n

import (
	"context"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode/utf8"

	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	sdk "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message/catalog"
)

func isNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func contextOK(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	return ctx.Err()
}
func identifier(s string) error {
	if s == "" || s == "." || s == ".." || len(s) > MaxIdentifierBytes {
		return ErrInvalidDefinition
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return ErrInvalidDefinition
		}
	}
	return nil
}

// ValidateDomain validates a domain identifier before authorization or storage.
func ValidateDomain(domain string) error { return identifier(domain) }
func revisionOK(rev int64) error {
	if rev < 0 || rev == math.MaxInt64 {
		return ErrConflict
	}
	return nil
}

func parseLocale(s string) (language.Tag, error) {
	if s == "" || len(s) > maxLocaleTagBytes {
		return language.Und, ErrInvalidDefinition
	}
	tag, e := language.Parse(s)
	if e != nil || tag == language.Und || tag.String() != s {
		return language.Und, ErrInvalidDefinition
	}
	return tag, nil
}
func configOK(c DomainConfig) error {
	if len(c.Locales) == 0 || len(c.Locales) > MaxLocales {
		return ErrInvalidConfig
	}
	seen := map[string]bool{}
	bundle := sdk.NewBundle(language.English)
	for _, s := range c.Locales {
		tag, e := parseLocale(s)
		if e != nil || seen[s] {
			return ErrInvalidConfig
		}
		seen[s] = true
		if e = bundle.AddMessages(tag, &sdk.Message{ID: "validate", Other: "ok"}); e != nil {
			return ErrInvalidConfig
		}
	}
	if !seen[c.DefaultLocale] {
		return ErrInvalidConfig
	}
	return nil
}

func formValues(f Forms) []string { return []string{f.Zero, f.One, f.Two, f.Few, f.Many, f.Other} }

var formCategories = []plural.Form{plural.Zero, plural.One, plural.Two, plural.Few, plural.Many, plural.Other}

// x/text's public catalog compiler rejects categories not present in a locale's
// CLDR rules. Probe that metadata once on catalog validation, never at lookup.
func requiredForms(tag language.Tag) []int {
	out := []int{}
	for i, form := range formCategories {
		b := catalog.NewBuilder()
		if b.Set(tag, "category", plural.Selectf(1, "", form, "x")) == nil {
			out = append(out, i)
		}
	}
	return out
}

func fields(src string) ([]string, error) {
	if strings.ContainsRune(src, '\x00') {
		return nil, ErrInvalidDefinition
	}
	if len(src) > MaxTemplateBytes || !utf8.ValidString(src) {
		return nil, ErrLimit
	}
	t, e := template.New("").Option("missingkey=error").Parse(src)
	if e != nil {
		return nil, ErrInvalidDefinition
	}
	seen := map[string]bool{}
	for _, n := range t.Tree.Root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			p := n.Pipe
			if p == nil || len(p.Decl) != 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
				return nil, ErrInvalidDefinition
			}
			f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(f.Ident) != 1 || identifier(f.Ident[0]) != nil {
				return nil, ErrInvalidDefinition
			}
			seen[f.Ident[0]] = true
		default:
			return nil, ErrInvalidDefinition
		}
	}
	if len(t.Templates()) != 1 || len(seen) > MaxParams {
		return nil, ErrInvalidDefinition
	}
	out := []string{}
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func formsOK(f Forms) ([]string, error) {
	var params []string
	for _, s := range formValues(f) {
		if s == "" {
			continue
		}
		p, e := fields(s)
		if e != nil {
			return nil, e
		}
		if params != nil && !slices.Equal(params, p) {
			return nil, ErrInvalidDefinition
		}
		params = p
	}
	return params, nil
}

func translationReady(k Key, loc string, f Forms) error {
	var required []int
	if k.Plural {
		tag, err := parseLocale(loc)
		if err != nil {
			return err
		}
		required = requiredForms(tag)
	}
	return translationReadyForForms(k, f, required)
}

func translationReadyForForms(k Key, f Forms, required []int) error {
	if f.Other == "" {
		return ErrIncomplete
	}
	expected, e := formsOK(k.Source)
	if e != nil {
		return e
	}
	actual, e := formsOK(f)
	if e != nil {
		return e
	}
	if !slices.Equal(expected, actual) {
		return ErrInvalidDefinition
	}
	values := formValues(f)
	if k.Plural {
		for _, i := range required {
			if values[i] == "" {
				return ErrIncomplete
			}
		}
	} else {
		for _, s := range values[:5] {
			if s != "" {
				return ErrInvalidDefinition
			}
		}
	}
	return nil
}

func definitionOK(d Definition) error {
	if identifier(d.Key) != nil || len(d.Description) > MaxDescriptionBytes || !utf8.ValidString(d.Description) || strings.ContainsRune(d.Description, '\x00') {
		return ErrInvalidDefinition
	}
	if d.Source.Other == "" {
		return ErrInvalidDefinition
	}
	params, e := formsOK(d.Source)
	if e != nil {
		return e
	}
	if !d.Plural && slices.Contains(params, "PluralCount") {
		return ErrInvalidDefinition
	}
	return nil
}

// NormalizeCatalog validates untrusted catalog data and returns an owned,
// canonical copy. complete requires publishable translations for every locale.
func NormalizeCatalog(c Catalog, complete bool) (Catalog, error) {
	if ValidateDomain(c.Domain) != nil || c.Revision <= 0 {
		return Catalog{}, ErrInvalidDefinition
	}
	if e := configOK(c.DomainConfig); e != nil {
		return Catalog{}, e
	}
	if len(c.Keys) > MaxKeys {
		return Catalog{}, ErrLimit
	}
	// Bound and validate strings before serialization/allocating an owned copy.
	budget := 0
	seen := map[string]bool{}
	allowed := map[string]bool{}
	formsByLocale := make(map[string][]int, len(c.Locales))
	for _, l := range c.Locales {
		allowed[l] = true
		if tag, err := parseLocale(l); err == nil {
			formsByLocale[l] = requiredForms(tag)
		}
	}
	for _, k := range c.Keys {
		if e := definitionOK(k.Definition); e != nil {
			return Catalog{}, e
		}
		if seen[k.Key] {
			return Catalog{}, ErrInvalidDefinition
		}
		seen[k.Key] = true
		budget += len(k.Description) + len(k.Key)
		for _, s := range formValues(k.Source) {
			budget += len(s)
		}
		if e := translationReadyForForms(k, k.Source, formsByLocale[c.DefaultLocale]); e != nil {
			return Catalog{}, e
		}
		if len(k.Translations) > MaxLocales {
			return Catalog{}, ErrLimit
		}
		for loc, tr := range k.Translations {
			if !allowed[loc] || loc == c.DefaultLocale {
				return Catalog{}, ErrInvalidDefinition
			}
			if _, e := formsOK(tr.Forms); e != nil {
				return Catalog{}, e
			}
			for _, s := range formValues(tr.Forms) {
				budget += len(s)
			}
			if tr.Reviewed {
				if e := translationReadyForForms(k, tr.Forms, formsByLocale[loc]); e != nil {
					return Catalog{}, e
				}
			}
		}
		if budget > MaxCatalogBytes {
			return Catalog{}, ErrLimit
		}
		if complete {
			for _, loc := range c.Locales {
				if loc == c.DefaultLocale {
					continue
				}
				tr, ok := k.Translations[loc]
				if !ok || !tr.Reviewed {
					return Catalog{}, ErrIncomplete
				}
			}
		}
	}
	b, e := jsoncodec.Marshal(c)
	if e != nil {
		return Catalog{}, ErrInvalidDefinition
	}
	if len(b) > MaxCatalogBytes {
		return Catalog{}, ErrLimit
	}
	var owned Catalog
	if e = jsoncodec.Unmarshal(b, &owned); e != nil {
		return Catalog{}, ErrInvalidDefinition
	}
	sort.Strings(owned.Locales)
	sort.Slice(owned.Keys, func(i, j int) bool { return owned.Keys[i].Key < owned.Keys[j].Key })
	if owned.Keys == nil {
		owned.Keys = []Key{}
	}
	return owned, nil
}

// ValidateState enforces the persistence contract, including published domain
// identity and revision fencing. It returns deep copies for safe ownership.
func ValidateState(domain string, s State) (State, error) {
	if s.Draft.Domain != domain {
		return State{}, ErrInvalidDefinition
	}
	draft, e := NormalizeCatalog(s.Draft, false)
	if e != nil {
		return State{}, e
	}
	out := State{Draft: draft}
	if s.Published != nil {
		if s.Published.Domain != domain || s.Published.Revision > s.Draft.Revision {
			return State{}, ErrInvalidDefinition
		}
		p, e := NormalizeCatalog(*s.Published, true)
		if e != nil {
			return State{}, e
		}
		out.Published = &p
	}
	encoded, err := jsoncodec.Marshal(out)
	if err != nil {
		return State{}, ErrInvalidDefinition
	}
	if len(encoded) > MaxCatalogBytes {
		return State{}, ErrLimit
	}
	return out, nil
}
