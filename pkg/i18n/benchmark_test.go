//go:build benchmark

package i18n_test

import (
	"context"
	"sync"
	"testing"

	"github.com/huynhanx03/go-common/pkg/i18n"
	"github.com/huynhanx03/go-common/pkg/settings"
	sdk "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func benchmarkClient(b *testing.B) *i18n.Client {
	b.Helper()
	catalog := i18n.Catalog{
		Domain: "billing", Revision: 1,
		DomainConfig: i18n.DomainConfig{DefaultLocale: "en", Locales: []string{"en", "vi"}},
		Keys: []i18n.Key{
			{Definition: i18n.Definition{Key: "hello", Source: i18n.Forms{Other: "Hello"}}, Translations: map[string]i18n.Translation{"vi": {Forms: i18n.Forms{Other: "Xin chào"}, Reviewed: true}}},
			{Definition: i18n.Definition{Key: "welcome", Source: i18n.Forms{Other: "Hello {{.Name}}"}}, Translations: map[string]i18n.Translation{"vi": {Forms: i18n.Forms{Other: "Chào {{.Name}}"}, Reviewed: true}}},
			{Definition: i18n.Definition{Key: "items", Plural: true, Source: i18n.Forms{One: "{{.Count}} item", Other: "{{.Count}} items"}}, Translations: map[string]i18n.Translation{"vi": {Forms: i18n.Forms{Other: "{{.Count}} sản phẩm"}, Reviewed: true}}},
			{Definition: i18n.Definition{Key: "total", Source: i18n.Forms{Other: "Total {{.Amount}}"}}, Translations: map[string]i18n.Translation{"vi": {Forms: i18n.Forms{Other: "Tổng {{.Amount}}"}, Reviewed: true}}},
		},
	}
	client, err := i18n.New(context.Background(), settings.I18n{Domain: "billing", CacheTTLSeconds: 3600}, i18n.ClientConfig{Cache: sharedCache(b), Source: i18n.SourceFunc(func(context.Context, string) (i18n.Catalog, error) {
		return catalog, nil
	})})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })
	return client
}

func runBenchmark(b *testing.B, parallel bool, operation func() error) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	if !parallel {
		for range b.N {
			if err := operation(); err != nil {
				b.Fatal(err)
			}
		}
		return
	}

	var (
		errOnce  sync.Once
		firstErr error
	)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := operation(); err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
		}
	})
	if firstErr != nil {
		b.Fatal(firstErr)
	}
}

func BenchmarkTranslate(b *testing.B) {
	client := benchmarkClient(b)
	cases := []struct {
		name    string
		key     string
		options i18n.Options
	}{
		{name: "static", key: "hello", options: i18n.Options{Locale: "en"}},
		{name: "params", key: "welcome", options: i18n.Options{Locale: "en", Params: map[string]any{"Name": "An"}}},
		{name: "plural", key: "items", options: i18n.Options{Locale: "en", Params: map[string]any{"Count": 3}, PluralCount: 3}},
		{name: "money", key: "total", options: i18n.Options{Locale: "en", Params: map[string]any{"Amount": i18n.Money{MinorUnits: 123456, Currency: "USD"}}}},
	}
	for _, test := range cases {
		for _, parallel := range []bool{false, true} {
			mode := "serial"
			if parallel {
				mode = "parallel"
			}
			b.Run(test.name+"/"+mode, func(b *testing.B) {
				runBenchmark(b, parallel, func() error {
					_, err := client.Translate(context.Background(), test.key, test.options)
					return err
				})
			})
		}
	}
	for _, parallel := range []bool{false, true} {
		mode := "serial"
		if parallel {
			mode = "parallel"
		}
		b.Run("direct_sdk_static/"+mode, func(b *testing.B) {
			bundle := sdk.NewBundle(language.English)
			bundle.MustAddMessages(language.English, &sdk.Message{ID: "hello", Other: "Hello"})
			localizer := sdk.NewLocalizer(bundle, "en")
			runBenchmark(b, parallel, func() error {
				_, err := localizer.Localize(&sdk.LocalizeConfig{MessageID: "hello"})
				return err
			})
		})
	}
}
