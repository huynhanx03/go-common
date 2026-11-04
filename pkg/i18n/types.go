// Package i18n provides local translation and domain-scoped catalog management.
package i18n

import "context"

type Forms struct {
	Zero  string `json:"zero,omitempty"`
	One   string `json:"one,omitempty"`
	Two   string `json:"two,omitempty"`
	Few   string `json:"few,omitempty"`
	Many  string `json:"many,omitempty"`
	Other string `json:"other"`
}

type DomainConfig struct {
	DefaultLocale string   `json:"default_locale"`
	Locales       []string `json:"locales"`
}

type Definition struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	Plural      bool   `json:"plural"`
	Source      Forms  `json:"source"`
}

type Translation struct {
	Forms    Forms `json:"forms"`
	Reviewed bool  `json:"reviewed"`
}

type Key struct {
	Definition
	Translations map[string]Translation `json:"translations"`
}

type Catalog struct {
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	DomainConfig
	Keys []Key `json:"keys"`
}

type State struct {
	Draft     Catalog  `json:"draft"`
	Published *Catalog `json:"published,omitempty"`
}

type Coverage struct {
	Locale      string   `json:"locale"`
	Total       int      `json:"total"`
	Translated  int      `json:"translated"`
	Reviewed    int      `json:"reviewed"`
	Missing     []string `json:"missing"`
	NeedsReview []string `json:"needs_review"`
}

type Query struct {
	Search, MissingLocale, After string
	Limit                        int
}
type Page struct {
	Keys     []Key  `json:"keys"`
	Next     string `json:"next,omitempty"`
	Revision int64  `json:"revision"`
}

// Money carries an exact display amount; no calculations or currency conversion
// take place in this package. USD minor units are cents; VND minor units are dong.
type Money struct {
	MinorUnits int64  `json:"minor_units"`
	Currency   string `json:"currency"`
}
type Options struct {
	Locale      string
	Params      map[string]any
	PluralCount any
}
type Result struct {
	Text     string `json:"text"`
	Locale   string `json:"locale"`
	Revision int64  `json:"revision"`
}

// Source supplies published routing metadata and a version-checked locale.
// LoadLocale includes only the selected locale and its default-language fallback.
type Source interface {
	Info(context.Context, string) (CatalogInfo, error)
	LoadLocale(context.Context, string, string, string) (Catalog, error)
}

// CatalogInfo routes requests without retaining message text.
type CatalogInfo struct {
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	Version  string `json:"version"`
	DomainConfig
}

// Store atomically persists a draft and its last publication. CompareAndSwap
// creates only at expected=0; all replacements require next.Draft.Revision=expected+1.
// Implementations return owned states and enforce the requested domain.
type Store interface {
	Read(context.Context, string) (State, error)
	CompareAndSwap(context.Context, string, int64, State) error
	Delete(context.Context, string, int64) error
	Domains(context.Context) ([]string, error)
}
