package i18n

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"slices"
)

// SourceFunc adapts an application's full-catalog loader at the source boundary.
// Remote clients should use HTTPSource to transfer only the requested locale.
type SourceFunc func(context.Context, string) (Catalog, error)

func (f SourceFunc) Info(ctx context.Context, domain string) (CatalogInfo, error) {
	catalog, err := f(ctx, domain)
	if err != nil {
		return CatalogInfo{}, err
	}
	return describeCatalog(catalog)
}

func (f SourceFunc) LoadLocale(ctx context.Context, domain, locale, version string) (Catalog, error) {
	catalog, err := f(ctx, domain)
	if err != nil {
		return Catalog{}, err
	}
	info, err := describeCatalog(catalog)
	if err != nil {
		return Catalog{}, err
	}
	if info.Version != version {
		return Catalog{}, ErrStaleRevision
	}
	if !slices.Contains(catalog.Locales, locale) {
		return Catalog{}, ErrNotFound
	}
	out := catalog
	out.Locales = []string{catalog.DefaultLocale}
	if locale != catalog.DefaultLocale {
		out.Locales = append(out.Locales, locale)
	}
	out.Keys = make([]Key, len(catalog.Keys))
	for i, key := range catalog.Keys {
		out.Keys[i] = Key{Definition: key.Definition, Translations: map[string]Translation{}}
		if locale != catalog.DefaultLocale {
			out.Keys[i].Translations[locale] = key.Translations[locale]
		}
	}
	return out, nil
}

func describeCatalog(catalog Catalog) (CatalogInfo, error) {
	catalog, err := NormalizeCatalog(catalog, true)
	if err != nil {
		return CatalogInfo{}, err
	}
	// Standard JSON sorts map keys, keeping this cache version stable across runs.
	encoded, err := stdjson.Marshal(catalog)
	if err != nil {
		return CatalogInfo{}, ErrInvalidDefinition
	}
	digest := sha256.Sum256(encoded)
	return CatalogInfo{Domain: catalog.Domain, Revision: catalog.Revision, Version: hex.EncodeToString(digest[:]), DomainConfig: catalog.DomainConfig}, nil
}

func validateInfo(domain string, info CatalogInfo) error {
	if info.Domain != domain || info.Revision <= 0 || !validVersion(info.Version) {
		return ErrInvalidDefinition
	}
	return configOK(info.DomainConfig)
}

func validVersion(version string) bool {
	if len(version) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(version)
	return err == nil
}
