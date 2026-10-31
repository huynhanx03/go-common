package unleash

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
)

// sourceError exposes only a safe operation/category while retaining causes for
// errors.Is. Source implementations may include sensitive data in their text.
type sourceError struct{ cause error }

func (sourceError) Error() string     { return "toggle: source failed" }
func (e sourceError) Unwrap() []error { return []error{toggle.ErrSource, e.cause} }

type sourceTransport struct {
	source   toggle.Source
	scope    toggle.Scope
	timeout  time.Duration
	lifetime context.Context
	accepted toggle.Snapshot
	first    bool
}

func (s *sourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Scheme != "http" || request.URL.Host != "toggle.invalid" || request.URL.Path != "/api/client/features" || request.URL.RawQuery != "" || request.URL.User != nil || request.URL.Fragment != "" {
		return nil, sourceError{toggle.ErrInvalidConfig}
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, sourceError{err}
	}
	if err := s.lifetime.Err(); err != nil {
		return nil, sourceError{err}
	}
	candidate := s.accepted
	if !s.first {
		loaded, err := s.source.Load(ctx, s.scope)
		if err != nil {
			return nil, sourceError{err}
		}
		if err := ctx.Err(); err != nil {
			return nil, sourceError{err}
		}
		candidate, err = toggle.NormalizeSnapshot(s.scope, loaded)
		if err != nil {
			return nil, sourceError{err}
		}
		if candidate.Revision < s.accepted.Revision || (candidate.Revision == s.accepted.Revision && !reflect.DeepEqual(candidate, s.accepted)) {
			return nil, sourceError{toggle.ErrStaleRevision}
		}
		if reflect.DeepEqual(candidate, s.accepted) {
			return response(request, http.StatusNotModified, nil), nil
		}
	}
	body, err := json.Marshal(mapSnapshot(candidate))
	if err != nil {
		return nil, sourceError{err}
	}
	s.accepted = candidate
	s.first = false
	return response(request, http.StatusOK, body), nil
}

func response(request *http.Request, status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}
}
