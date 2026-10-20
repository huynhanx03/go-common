package middlewares

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

type CSRFVerifier interface {
	Verify(ctx context.Context, request *http.Request) error
}

// CSRF verifies configured unsafe methods. When safeMethods is empty, GET,
// HEAD, and OPTIONS are considered safe.
func CSRF(verifier CSRFVerifier, safeMethods ...string) (gin.HandlerFunc, error) {
	if isNilCSRFVerifier(verifier) {
		return nil, errors.New("csrf: nil verifier")
	}
	if len(safeMethods) == 0 {
		safeMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	}
	if len(safeMethods) > 16 {
		return nil, errors.New("csrf: too many safe methods")
	}
	safe := make(map[string]struct{}, len(safeMethods))
	for _, method := range safeMethods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !validHTTPToken(method) {
			return nil, errors.New("csrf: invalid safe method")
		}
		safe[method] = struct{}{}
	}
	return func(c *gin.Context) {
		if _, exists := safe[c.Request.Method]; exists {
			c.Next()
			return
		}
		if err := verifier.Verify(c.Request.Context(), c.Request); err != nil {
			if c.Request.Context().Err() != nil {
				c.Abort()
				return
			}
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(
				apperr.CodeForbidden,
				"csrf verification failed",
				nil,
			))
			c.Abort()
			return
		}
		c.Next()
	}, nil
}

func isNilCSRFVerifier(verifier CSRFVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
