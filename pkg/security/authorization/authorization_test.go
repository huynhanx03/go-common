package authorization_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

func TestRequestValidation(t *testing.T) {
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

	validRequests := []struct {
		name    string
		request authorization.Request
	}{
		{name: "legacy request without optional scope or resource ID", request: valid},
		{
			name: "scoped request with resource ID",
			request: func() authorization.Request {
				request := valid
				request.Scope = authorization.Scope{Type: "tenant-2_team", ID: "acme"}
				request.ResourceID = "contest:1"
				return request
			}(),
		},
		{
			name: "anonymous request shape",
			request: func() authorization.Request {
				request := valid
				request.Principal = authentication.Anonymous()
				return request
			}(),
		},
	}
	for _, test := range validRequests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.request.Validate(now); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	invalidRequests := []struct {
		name   string
		mutate func(*authorization.Request)
	}{
		{
			name: "missing authenticated subject",
			mutate: func(request *authorization.Request) {
				request.Principal.Subject = ""
			},
		},
		{
			name: "expired principal",
			mutate: func(request *authorization.Request) {
				request.Principal.AuthenticatedUntil = now
			},
		},
		{
			name: "missing resource",
			mutate: func(request *authorization.Request) {
				request.Resource = ""
			},
		},
		{
			name: "padded resource",
			mutate: func(request *authorization.Request) {
				request.Resource = " document"
			},
		},
		{
			name: "control character in resource",
			mutate: func(request *authorization.Request) {
				request.Resource = "doc\nument"
			},
		},
		{
			name: "resource exceeds limit",
			mutate: func(request *authorization.Request) {
				request.Resource = strings.Repeat("r", 257)
			},
		},
		{
			name: "padded resource ID",
			mutate: func(request *authorization.Request) {
				request.ResourceID = " contest:1"
			},
		},
		{
			name: "resource ID exceeds limit",
			mutate: func(request *authorization.Request) {
				request.ResourceID = strings.Repeat("r", 257)
			},
		},
		{
			name: "missing action",
			mutate: func(request *authorization.Request) {
				request.Action = ""
			},
		},
		{
			name: "control character in action",
			mutate: func(request *authorization.Request) {
				request.Action = "re\nad"
			},
		},
		{
			name: "action exceeds limit",
			mutate: func(request *authorization.Request) {
				request.Action = strings.Repeat("a", 129)
			},
		},
		{
			name: "invalid scope type",
			mutate: func(request *authorization.Request) {
				request.Scope = authorization.Scope{Type: "Tenant", ID: "acme"}
			},
		},
		{
			name: "invalid scope ID",
			mutate: func(request *authorization.Request) {
				request.Scope = authorization.Scope{Type: "tenant", ID: "ac\nme"}
			},
		},
	}
	for _, test := range invalidRequests {
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

func TestScopeValidationAndKey(t *testing.T) {
	t.Parallel()

	valid := authorization.Scope{Type: "tenant-2_team", ID: "acme"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, want := valid.Key(), "tenant-2_team:acme"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}

	tests := []struct {
		name  string
		scope authorization.Scope
	}{
		{name: "missing type", scope: authorization.Scope{ID: "acme"}},
		{name: "type exceeds limit", scope: authorization.Scope{Type: strings.Repeat("a", 65), ID: "acme"}},
		{name: "uppercase type", scope: authorization.Scope{Type: "Tenant", ID: "acme"}},
		{name: "digit at start of type", scope: authorization.Scope{Type: "1tenant", ID: "acme"}},
		{name: "separator at start of type", scope: authorization.Scope{Type: "_tenant", ID: "acme"}},
		{name: "missing ID", scope: authorization.Scope{Type: "tenant"}},
		{name: "ID exceeds limit", scope: authorization.Scope{Type: "tenant", ID: strings.Repeat("i", 257)}},
		{name: "padded ID", scope: authorization.Scope{Type: "tenant", ID: " acme"}},
		{name: "control character in ID", scope: authorization.Scope{Type: "tenant", ID: "ac\nme"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.scope.Validate(); !errors.Is(err, authorization.ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestDefaultSystemScopeIsUsable(t *testing.T) {
	t.Parallel()

	scope := authorization.DefaultSystemScope()
	if err := scope.Validate(); err != nil {
		t.Fatalf("DefaultSystemScope().Validate() error = %v", err)
	}
	if scope.Key() == "" {
		t.Fatal("DefaultSystemScope().Key() is empty")
	}
}
