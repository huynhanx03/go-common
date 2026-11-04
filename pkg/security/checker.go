package security

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

// Config injects the engines used by the checker. An engine may be omitted
// when its operation is not used.
type Config struct {
	Verifier   authentication.Verifier
	Authorizer authorization.Authorizer
	Clock      func() time.Time
}

type Checker struct {
	verifier   authentication.Verifier
	authorizer authorization.Authorizer
	clock      func() time.Time
}

type Permission struct {
	Scope      authorization.Scope
	Resource   string
	ResourceID string
	Action     string
}

type Request struct {
	Token       string
	Purpose     authentication.TokenPurpose
	Permissions []Permission
}

type Result struct {
	Principal authentication.Principal
	Decision  authorization.Decision
}

func NewChecker(config Config) *Checker {
	if nilDependency(config.Verifier) {
		config.Verifier = nil
	}
	if nilDependency(config.Authorizer) {
		config.Authorizer = nil
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Checker{verifier: config.Verifier, authorizer: config.Authorizer, clock: config.Clock}
}

// Check authenticates a token and requires at least one allowed permission.
// Failed checks never return an authenticated principal.
func (c *Checker) Check(ctx context.Context, request Request) (Result, error) {
	principal, err := c.Authenticate(ctx, request.Token, request.Purpose)
	if err != nil {
		return Result{}, err
	}
	decision, err := c.Authorize(ctx, principal, request.Permissions...)
	if err != nil {
		return Result{Decision: decision}, err
	}
	return Result{Principal: principal, Decision: decision}, nil
}

func (c *Checker) Authenticate(ctx context.Context, token string, purpose authentication.TokenPurpose) (authentication.Principal, error) {
	if ctx == nil {
		return authentication.Anonymous(), ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return authentication.Anonymous(), err
	}
	if c == nil || c.verifier == nil {
		return authentication.Anonymous(), ErrInvalidConfiguration
	}
	if purpose.Validate() != nil {
		return authentication.Anonymous(), ErrInvalidRequest
	}
	if token == "" {
		return authentication.Anonymous(), ErrUnauthenticated
	}
	principal, err := c.verifier.Verify(ctx, token, purpose)
	if contextError := ctx.Err(); contextError != nil {
		return authentication.Anonymous(), contextError
	}
	if err != nil {
		return authentication.Anonymous(), errors.Join(ErrUnauthenticated, err)
	}
	if err := validatePrincipal(principal, c.clock()); err != nil {
		return authentication.Anonymous(), err
	}
	return principal, nil
}

// Principal reads a verified identity from context and checks its lifetime before
// application code resolves permissions. Authorize rechecks it after resolution.
func (c *Checker) Principal(ctx context.Context) (authentication.Principal, error) {
	if ctx == nil {
		return authentication.Anonymous(), ErrUnauthenticated
	}
	if err := ctx.Err(); err != nil {
		return authentication.Anonymous(), err
	}
	if c == nil {
		return authentication.Anonymous(), ErrInvalidConfiguration
	}
	principal, _ := authentication.FromContext(ctx)
	if err := validatePrincipal(principal, c.clock()); err != nil {
		return authentication.Anonymous(), err
	}
	return principal, nil
}

// Authorize evaluates any-of requirements. All requirements are validated before
// evaluation; an evaluator error stops the check instead of trying another rule.
func (c *Checker) Authorize(ctx context.Context, principal authentication.Principal, permissions ...Permission) (authorization.Decision, error) {
	if ctx == nil {
		return authorization.Decision{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return authorization.Decision{}, err
	}
	if c == nil || c.authorizer == nil {
		return authorization.Decision{}, ErrInvalidConfiguration
	}
	now := c.clock()
	if err := validatePrincipal(principal, now); err != nil {
		return authorization.Decision{}, err
	}
	if len(permissions) == 0 {
		return authorization.Decision{}, ErrInvalidRequest
	}
	for _, permission := range permissions {
		if err := permission.request(principal).Validate(now); err != nil {
			return authorization.Decision{}, errors.Join(ErrInvalidRequest, err)
		}
	}
	decision := authorization.Decision{Reason: authorization.ReasonPolicyDenied}
	for _, permission := range permissions {
		var err error
		decision, err = c.authorizer.Authorize(ctx, permission.request(principal))
		if contextError := ctx.Err(); contextError != nil {
			err = contextError
		}
		if err != nil {
			decision.Allowed = false
			decision.Reason = authorization.ReasonEvaluationError
			return decision, errors.Join(authorization.ErrEvaluation, err)
		}
		if decision.Allowed {
			return decision, nil
		}
	}
	return decision, ErrForbidden
}

func validatePrincipal(principal authentication.Principal, now time.Time) error {
	if !principal.Authenticated {
		return ErrUnauthenticated
	}
	if err := principal.Validate(now); err != nil {
		return errors.Join(ErrUnauthenticated, err)
	}
	return nil
}

func (p Permission) request(principal authentication.Principal) authorization.Request {
	return authorization.Request{
		Principal: principal, Scope: p.Scope, Resource: p.Resource,
		ResourceID: p.ResourceID, Action: p.Action,
	}
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
