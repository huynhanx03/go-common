package gin

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/huynhanx03/go-common/pkg/i18n"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
)

// Authorizer binds an operation to one catalog domain. Services can map these
// actions to their existing authorization rules.
type Authorizer interface {
	Authorize(context.Context, string, string) error
}

type AuthorizerFunc func(context.Context, string, string) error

func (f AuthorizerFunc) Authorize(ctx context.Context, domain, action string) error {
	return f(ctx, domain, action)
}

// SecurityAuthorizer adapts go-common authentication and authorization to the
// i18n domain scope. Authentication middleware must add the principal to context.
type SecurityAuthorizer struct{ authorizer authorization.Authorizer }

func NewSecurityAuthorizer(authorizer authorization.Authorizer) (*SecurityAuthorizer, error) {
	if nilValue(authorizer) {
		return nil, i18n.ErrInvalidConfig
	}
	return &SecurityAuthorizer{authorizer: authorizer}, nil
}

func (a *SecurityAuthorizer) Authorize(ctx context.Context, domain, action string) error {
	if a == nil || nilValue(a.authorizer) || i18n.ValidateDomain(domain) != nil || ctx == nil {
		return i18n.ErrInvalidConfig
	}
	principal, ok := authentication.FromContext(ctx)
	if !ok || !principal.Authenticated || principal.Validate(time.Now()) != nil {
		return ErrUnauthenticated
	}
	decision, err := a.authorizer.Authorize(ctx, authorization.Request{
		Principal:  principal,
		Scope:      authorization.Scope{Type: "i18n", ID: domain},
		Resource:   "i18n",
		ResourceID: domain,
		Action:     action,
	})
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return ref.IsNil()
	}
	return false
}

var _ Authorizer = (*SecurityAuthorizer)(nil)
