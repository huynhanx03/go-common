package authorization_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

func TestRequestValidationFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	valid := authorization.Request{
		Principal: authentication.Principal{
			Authenticated:      true,
			Subject:            "user:1",
			AuthenticatedUntil: now.Add(time.Minute),
		},
		Resource: "document",
		Action:   "read",
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*authorization.Request)
	}{
		{name: "expired principal", mutate: func(request *authorization.Request) { request.Principal.AuthenticatedUntil = now }},
		{name: "missing resource", mutate: func(request *authorization.Request) { request.Resource = "" }},
		{name: "padded resource", mutate: func(request *authorization.Request) { request.Resource = " document" }},
		{name: "control action", mutate: func(request *authorization.Request) { request.Action = "read\n" }},
		{name: "oversized action", mutate: func(request *authorization.Request) { request.Action = string(make([]byte, 129)) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := valid
			test.mutate(&request)
			if err := request.Validate(now); !errors.Is(err, authorization.ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

type staticAuthorizer struct{}

func (staticAuthorizer) Authorize(
	context.Context,
	authorization.Request,
) (authorization.Decision, error) {
	return authorization.Decision{
		Allowed:  false,
		Reason:   authorization.ReasonPolicyDenied,
		Revision: 1,
	}, nil
}

var _ authorization.Authorizer = staticAuthorizer{}
