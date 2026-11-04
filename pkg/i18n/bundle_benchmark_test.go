//go:build benchmark

package i18n

import (
	"fmt"
	"testing"
)

func BenchmarkCompileCatalog(b *testing.B) {
	for _, keyCount := range []int{10, 1000} {
		b.Run(fmt.Sprintf("keys_%d", keyCount), func(b *testing.B) {
			catalog := benchmarkRuntimeCatalog(keyCount)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := compileCatalog(catalog.Domain, catalog); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkRuntimeCatalog(keyCount int) Catalog {
	catalog := Catalog{
		Domain: "benchmark", Revision: 1,
		DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en", "vi"}},
		Keys:         make([]Key, keyCount),
	}
	for i := range catalog.Keys {
		catalog.Keys[i] = Key{
			Definition: Definition{
				Key:    fmt.Sprintf("message.%d", i),
				Source: Forms{Other: "Hello {{.Name}}"},
			},
			Translations: map[string]Translation{
				"vi": {Forms: Forms{Other: "Xin chào {{.Name}}"}, Reviewed: true},
			},
		}
	}
	return catalog
}
