package authorization

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

const (
	maxResourceBytes = 256
	maxActionBytes   = 128
)

type Request struct {
	Principal authentication.Principal
	Resource  string
	Action    string
}

func (r Request) Validate(now time.Time) error {
	if err := r.Principal.Validate(now); err != nil {
		return fmt.Errorf("%w: principal", ErrInvalidRequest)
	}
	if err := validateTerm(r.Resource, maxResourceBytes); err != nil {
		return fmt.Errorf("%w: resource", ErrInvalidRequest)
	}
	if err := validateTerm(r.Action, maxActionBytes); err != nil {
		return fmt.Errorf("%w: action", ErrInvalidRequest)
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
