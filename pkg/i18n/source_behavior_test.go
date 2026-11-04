package i18n

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (failingBody) Close() error               { return nil }

func TestSourceFuncReturnsVersionedLocaleProjections(t *testing.T) {
	catalog := Catalog{
		Domain: "billing", Revision: 4,
		DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}},
		Keys: []Key{{
			Definition:   Definition{Key: "welcome", Source: Forms{Other: "Xin chào"}},
			Translations: map[string]Translation{"en": {Forms: Forms{Other: "Hello"}, Reviewed: true}},
		}},
	}
	source := SourceFunc(func(context.Context, string) (Catalog, error) { return catalog, nil })
	info, err := source.Info(context.Background(), "billing")
	if err != nil || validateInfo("billing", info) != nil {
		t.Fatalf("catalog info=%+v err=%v", info, err)
	}
	localized, err := source.LoadLocale(context.Background(), "billing", "en", info.Version)
	if err != nil || len(localized.Locales) != 2 || localized.Keys[0].Translations["en"].Forms.Other != "Hello" {
		t.Fatalf("English projection=%+v err=%v", localized, err)
	}
	defaultCatalog, err := source.LoadLocale(context.Background(), "billing", "vi", info.Version)
	if err != nil || len(defaultCatalog.Locales) != 1 || len(defaultCatalog.Keys[0].Translations) != 0 {
		t.Fatalf("default projection=%+v err=%v", defaultCatalog, err)
	}
	if _, err := source.LoadLocale(context.Background(), "billing", "en", strings.Repeat("0", 64)); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale source version: %v", err)
	}
	if _, err := source.LoadLocale(context.Background(), "billing", "fr", info.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsupported locale: %v", err)
	}

	backendErr := errors.New("catalog database unavailable")
	failing := SourceFunc(func(context.Context, string) (Catalog, error) { return Catalog{}, backendErr })
	if _, err := failing.Info(context.Background(), "billing"); !errors.Is(err, backendErr) {
		t.Fatalf("Info backend error=%v", err)
	}
	if _, err := failing.LoadLocale(context.Background(), "billing", "en", info.Version); !errors.Is(err, backendErr) {
		t.Fatalf("LoadLocale backend error=%v", err)
	}
	invalid := SourceFunc(func(context.Context, string) (Catalog, error) { return Catalog{}, nil })
	if _, err := invalid.Info(context.Background(), "billing"); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid catalog info=%v", err)
	}
	if _, err := invalid.LoadLocale(context.Background(), "billing", "en", info.Version); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid catalog locale projection=%v", err)
	}
}

func TestCatalogVersionValidationRejectsMismatchedMetadata(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	if !validVersion(validDigest) {
		t.Fatal("valid digest rejected")
	}
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if validVersion(value) {
			t.Errorf("invalid digest accepted: %q", value)
		}
	}
	valid := CatalogInfo{Domain: "billing", Revision: 1, Version: validDigest, DomainConfig: DomainConfig{DefaultLocale: "en", Locales: []string{"en"}}}
	if err := validateInfo("billing", valid); err != nil {
		t.Fatalf("valid catalog metadata: %v", err)
	}
	for name, mutate := range map[string]func(*CatalogInfo){
		"wrong domain":    func(info *CatalogInfo) { info.Domain = "other" },
		"zero revision":   func(info *CatalogInfo) { info.Revision = 0 },
		"invalid digest":  func(info *CatalogInfo) { info.Version = "bad" },
		"invalid locales": func(info *CatalogInfo) { info.DomainConfig = DomainConfig{} },
	} {
		t.Run(name, func(t *testing.T) {
			info := valid
			mutate(&info)
			if err := validateInfo("billing", info); err == nil {
				t.Fatalf("invalid metadata accepted: %v", err)
			}
		})
	}
}

func TestHTTPSourceValidatesConfigurationAndPublishedResponses(t *testing.T) {
	for _, base := range []string{"", "://bad", "ftp://catalog.local", "http:///path", "http://user:secret@catalog.local", "http://catalog.local?debug=true", "http://catalog.local#fragment"} {
		if _, err := NewHTTPSource(base, nil); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("invalid source URL %q accepted: %v", base, err)
		}
	}

	info := CatalogInfo{
		Domain: "billing", Revision: 4, Version: strings.Repeat("a", 64),
		DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}},
	}
	projection := Catalog{
		Domain: "billing", Revision: 4,
		DomainConfig: DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}},
		Keys:         []Key{{Definition: Definition{Key: "welcome", Source: Forms{Other: "Xin chào"}}, Translations: map[string]Translation{"en": {Forms: Forms{Other: "Hello"}, Reviewed: true}}}},
	}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("source Accept header=%q", r.Header.Get("Accept"))
		}
		var body []byte
		switch {
		case strings.HasSuffix(r.URL.Path, "/catalog/manifest"):
			body, _ = json.Marshal(info)
		case strings.HasSuffix(r.URL.Path, "/catalog/locales/en"):
			if r.URL.Query().Get("version") != info.Version {
				t.Errorf("locale version query=%q", r.URL.Query().Get("version"))
			}
			body, _ = json.Marshal(projection)
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	source, err := NewHTTPSource("http://catalog.local/api/", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := source.Info(context.Background(), "billing")
	if err != nil || gotInfo.Revision != info.Revision {
		t.Fatalf("HTTP manifest=%+v err=%v", gotInfo, err)
	}
	got, err := source.LoadLocale(context.Background(), "billing", "en", info.Version)
	if err != nil || got.Keys[0].Translations["en"].Forms.Other != "Hello" {
		t.Fatalf("HTTP locale=%+v err=%v", got, err)
	}
	if _, err := source.Info(context.Background(), "bad domain"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid domain request: %v", err)
	}
	if _, err := source.LoadLocale(context.Background(), "billing", "en_US", info.Version); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid locale request: %v", err)
	}
	if _, err := source.LoadLocale(context.Background(), "billing", "en", "bad"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid version request: %v", err)
	}
	if _, err := (*HTTPSource)(nil).Info(context.Background(), "billing"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil source receiver: %v", err)
	}
}

func TestHTTPSourceUsesDefaultTransportWhenNoClientIsInjected(t *testing.T) {
	source, err := NewHTTPSource("https://catalog.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Info(ctx, "billing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("default HTTP client did not preserve request cancellation: %v", err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusPreconditionFailed, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	source, err = NewHTTPSource("https://catalog.example", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.LoadLocale(context.Background(), "billing", "en", strings.Repeat("a", 64)); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("locale source HTTP failure=%v", err)
	}
}

func TestHTTPSourceMapsTransportStatusAndBodyFailures(t *testing.T) {
	response := func(status int, body io.ReadCloser) *http.Response {
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}
	}
	cases := []struct {
		name      string
		roundTrip roundTripFunc
		want      error
	}{
		{name: "transport", roundTrip: func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") }, want: ErrSource},
		{name: "stale revision", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusPreconditionFailed, io.NopCloser(strings.NewReader(""))), nil
		}, want: ErrStaleRevision},
		{name: "missing", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusNotFound, io.NopCloser(strings.NewReader(""))), nil
		}, want: ErrNotFound},
		{name: "server error", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, io.NopCloser(strings.NewReader(""))), nil
		}, want: ErrSource},
		{name: "read failure", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, failingBody{err: errors.New("socket read failed")}), nil
		}, want: ErrSource},
		{name: "bad json", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, io.NopCloser(strings.NewReader("{"))), nil
		}, want: ErrInvalidDefinition},
		{name: "oversized", roundTrip: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, io.NopCloser(strings.NewReader("too large"))), nil
		}, want: ErrLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &HTTPSource{base: &url.URL{Scheme: "http", Host: "catalog.local"}, client: &http.Client{Transport: test.roundTrip}}
			var result CatalogInfo
			err := source.get(context.Background(), "billing", "catalog/manifest", "", &result, 4)
			if !errors.Is(err, test.want) {
				t.Fatalf("HTTP source error=%v, want category %v", err, test.want)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	source := &HTTPSource{base: &url.URL{Scheme: "http", Host: "catalog.local"}, client: http.DefaultClient}
	if err := source.get(canceled, "billing", "catalog/manifest", "", &CatalogInfo{}, maxInfoBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error=%v", err)
	}
	if err := (*HTTPSource)(nil).get(context.Background(), "billing", "catalog/manifest", "", &CatalogInfo{}, maxInfoBytes); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil source get error=%v", err)
	}
	if err := (&HTTPSource{base: &url.URL{Scheme: "http", Host: "bad host"}, client: http.DefaultClient}).get(context.Background(), "billing", "catalog/manifest", "", &CatalogInfo{}, maxInfoBytes); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("malformed internal endpoint error=%v", err)
	}
	if err := (&HTTPSource{base: &url.URL{Scheme: "http", Host: "catalog.local"}}).get(context.Background(), "billing", "catalog/manifest", "", &CatalogInfo{}, maxInfoBytes); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("source without client error=%v", err)
	}
}

func TestHTTPSourceRejectsMalformedManifest(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"domain":"other","revision":0,"version":"bad","default_locale":"en","locales":["en"]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	source, err := NewHTTPSource("https://catalog.local", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Info(context.Background(), "billing"); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid manifest accepted: %v", err)
	}
}
