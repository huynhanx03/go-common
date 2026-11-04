package i18n

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"golang.org/x/text/language"
)

type cachedInfo struct {
	Info     CatalogInfo `json:"info"`
	LoadedAt int64       `json:"loaded_at"`
}

type localeIndex struct {
	info       CatalogInfo
	validUntil int64
	epoch      uint64
	matcher    language.Matcher
	locales    []string
	exact      map[string]bool
}

func (c *Client) loadIndex(ctx context.Context, epoch uint64) (*localeIndex, error) {
	if current := c.index.Load(); current != nil && current.epoch == epoch && current.validUntil > time.Now().UnixMilli() {
		return current, nil
	}
	key := manifestKeyPrefix + c.domain
	for range maxLoadAttempts {
		data, err := cache.Fetch(ctx, c.cache, key, c.ttl, func(ctx context.Context) (cachedInfo, error) {
			callCtx, cancel := context.WithTimeout(ctx, c.sourceTimeout)
			defer cancel()
			info, err := c.source.Info(callCtx, c.domain)
			if err != nil {
				return cachedInfo{}, backendError(err)
			}
			if err = validateInfo(c.domain, info); err != nil {
				return cachedInfo{}, err
			}
			return cachedInfo{Info: info, LoadedAt: time.Now().UnixMilli()}, nil
		}, cache.WithoutRefresh())
		if err != nil {
			return nil, err
		}
		if err = validateInfo(c.domain, data.Info); err != nil {
			return nil, err
		}
		validUntil := data.LoadedAt + c.ttl.Milliseconds()
		if validUntil <= time.Now().UnixMilli() {
			if err := c.cache.Delete(ctx, key); err != nil {
				return nil, sourceError(err)
			}
			continue
		}
		info := data.Info
		names := []string{info.DefaultLocale}
		for _, name := range info.Locales {
			if name != info.DefaultLocale {
				names = append(names, name)
			}
		}
		tags := make([]language.Tag, len(names))
		exact := make(map[string]bool, len(names))
		for i, name := range names {
			tags[i], _ = parseLocale(name)
			exact[name] = true
		}
		index := &localeIndex{info: info, validUntil: validUntil, epoch: epoch, matcher: language.NewMatcher(tags), locales: names, exact: exact}
		if c.epoch.Load() != epoch {
			if err := c.cache.Delete(ctx, key); err != nil {
				return nil, sourceError(err)
			}
			return nil, ErrStaleRevision
		}
		c.index.Store(index)
		return index, nil
	}
	return nil, ErrStaleRevision
}

func chooseLocale(index *localeIndex, preference string) string {
	if index.exact[preference] {
		return preference
	}
	tags, _, err := language.ParseAcceptLanguage(preference)
	if err != nil || len(tags) == 0 {
		return index.info.DefaultLocale
	}
	_, i, _ := index.matcher.Match(tags...)
	return index.locales[i]
}

func (c *Client) loadBundle(ctx context.Context, index *localeIndex, locale string) (*compiledCatalog, error) {
	suffix := c.domain + ":" + index.info.Version + ":" + locale
	return cache.Fetch(ctx, c.cache, bundleKeyPrefix+suffix, c.ttl, func(ctx context.Context) (*compiledCatalog, error) {
		catalog, err := cache.Fetch(ctx, c.cache, localeKeyPrefix+suffix, c.ttl, func(ctx context.Context) (Catalog, error) {
			callCtx, cancel := context.WithTimeout(ctx, c.sourceTimeout)
			defer cancel()
			value, err := c.source.LoadLocale(callCtx, c.domain, locale, index.info.Version)
			if err != nil {
				return Catalog{}, backendError(err)
			}
			return validateProjection(index.info, locale, value)
		}, cache.WithoutRefresh())
		if err != nil {
			return nil, err
		}
		catalog, err = validateProjection(index.info, locale, catalog)
		if err != nil {
			return nil, err
		}
		return compileCatalog(c.domain, catalog)
	}, cache.L1Only(), cache.WithoutRefresh())
}

func validateProjection(info CatalogInfo, locale string, catalog Catalog) (Catalog, error) {
	if catalog.Domain != info.Domain || catalog.Revision != info.Revision {
		return Catalog{}, ErrStaleRevision
	}
	locales := []string{info.DefaultLocale}
	if locale != info.DefaultLocale {
		locales = append(locales, locale)
	}
	slices.Sort(locales)
	got := slices.Clone(catalog.Locales)
	slices.Sort(got)
	if catalog.DefaultLocale != info.DefaultLocale || !slices.Equal(got, locales) {
		return Catalog{}, ErrInvalidDefinition
	}
	return NormalizeCatalog(catalog, true)
}

// Translate loads a locale on its first use and renders subsequent calls from L1.
func (c *Client) Translate(ctx context.Context, key string, options Options) (Result, error) {
	if c.closed.Load() {
		return Result{}, ErrClosed
	}
	if err := contextOK(ctx); err != nil {
		return Result{}, err
	}
	if identifier(key) != nil || len(options.Locale) > MaxLocaleBytes {
		return Result{}, ErrInvalidParams
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.live, cancel)
	defer func() { stop(); cancel() }()
	for range maxLoadAttempts {
		if c.closed.Load() {
			return Result{}, ErrClosed
		}
		epoch := c.epoch.Load()
		index, err := c.loadIndex(callCtx, epoch)
		if err != nil {
			if errors.Is(err, ErrStaleRevision) {
				continue
			}
			return Result{}, err
		}
		locale := chooseLocale(index, options.Locale)
		bundle, err := c.loadBundle(callCtx, index, locale)
		if errors.Is(err, ErrStaleRevision) {
			if err = c.invalidate(callCtx); err != nil {
				return Result{}, err
			}
			continue
		}
		c.stateMu.RLock()
		if c.closed.Load() {
			c.stateMu.RUnlock()
			return Result{}, ErrClosed
		}
		if c.epoch.Load() != epoch {
			c.stateMu.RUnlock()
			if err := c.cache.Delete(callCtx, manifestKeyPrefix+c.domain); err != nil {
				return Result{}, sourceError(err)
			}
			continue
		}
		if err != nil {
			c.stateMu.RUnlock()
			return Result{}, err
		}
		result, err := render(bundle, locale, key, options)
		c.stateMu.RUnlock()
		return result, err
	}
	return Result{}, ErrStaleRevision
}
