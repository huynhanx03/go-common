package authorization

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

// Scope identifies the authorization domain a request and its policy belong to.
// Type is a stable application-defined kind (for example "system" or "tenant");
// ID is an opaque identifier resolved from trusted application state.
type Scope struct {
	Type string
	ID   string
}

// DefaultSystemScope is the explicit domain used by single-scope services and
// global system roles. It avoids treating an empty domain as a wildcard.
func DefaultSystemScope() Scope {
	return Scope{Type: defaultSystemScopeType, ID: defaultSystemScopeID}
}

func (s Scope) Validate() error {
	if s.Type == "" || len(s.Type) > maxScopeTypeBytes {
		return ErrInvalidRequest
	}
	for index, character := range s.Type {
		if !(character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' && character <= '9' ||
			index > 0 && (character == '_' || character == '-')) {
			return ErrInvalidRequest
		}
	}
	if err := validateTerm(s.ID, maxScopeIDBytes); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

// Key returns the unambiguous domain token used by the shared Casbin model.
func (s Scope) Key() string {
	return s.Type + scopeKeySeparator + s.ID
}

type Request struct {
	Principal  authentication.Principal
	Scope      Scope
	Resource   string
	ResourceID string
	Action     string
}

func (r Request) Validate(now time.Time) error {
	if err := r.Principal.Validate(now); err != nil {
		return fmt.Errorf("%w: principal", ErrInvalidRequest)
	}
	if err := validateTerm(r.Resource, maxResourceBytes); err != nil {
		return fmt.Errorf("%w: resource", ErrInvalidRequest)
	}
	if r.ResourceID != "" {
		if err := validateTerm(r.ResourceID, maxResourceIDBytes); err != nil {
			return fmt.Errorf("%w: resource id", ErrInvalidRequest)
		}
	}
	if err := validateTerm(r.Action, maxActionBytes); err != nil {
		return fmt.Errorf("%w: action", ErrInvalidRequest)
	}
	// Scope is optional only for legacy three-field models. Scoped models check
	// it at evaluation time and fail closed when it is absent or malformed.
	if r.Scope.Type != "" || r.Scope.ID != "" {
		if err := r.Scope.Validate(); err != nil {
			return fmt.Errorf("%w: scope", ErrInvalidRequest)
		}
	}
	return nil
}

type Decision struct {
	Allowed  bool
	Reason   string
	Revision int64
}

type Authorizer interface {
	Authorize(ctx context.Context, request Request) (Decision, error)
}

func validateTerm(value string, maximum int) error {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return ErrInvalidRequest
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ErrInvalidRequest
		}
	}
	return nil
}
