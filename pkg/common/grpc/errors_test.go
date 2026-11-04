package grpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/security"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

func TestStatusErrorMapsTypedFailuresWithoutLeakingCauses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "canceled", err: context.Canceled, code: codes.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{name: "authentication", err: authentication.ErrInvalidToken, code: codes.Unauthenticated},
		{name: "checker authentication", err: errors.Join(security.ErrUnauthenticated, status.Error(codes.Internal, "token=secret")), code: codes.Unauthenticated},
		{name: "checker forbidden", err: security.ErrForbidden, code: codes.PermissionDenied},
		{name: "checker configuration", err: security.ErrInvalidConfiguration, code: codes.Internal},
		{name: "checker invalid permission", err: errors.Join(security.ErrInvalidRequest, authorization.ErrInvalidRequest), code: codes.Internal},
		{name: "evaluator error wraps denial", err: errors.Join(authorization.ErrEvaluation, security.ErrForbidden), code: codes.Internal},
		{name: "evaluator error wraps status", err: errors.Join(authorization.ErrEvaluation, status.Error(codes.Internal, "token=secret")), code: codes.Internal},
		{name: "invalid argument", err: apperr.New(apperr.CodeParamInvalid, "unsafe detail", errors.New("token=secret")), code: codes.InvalidArgument},
		{name: "account not found", err: apperr.New(apperr.CodeAccountNotFound, "unsafe detail", nil), code: codes.NotFound},
		{name: "forbidden", err: apperr.New(apperr.CodeForbidden, "unsafe detail", nil), code: codes.PermissionDenied},
		{name: "not found", err: apperr.New(apperr.CodeNotFound, "unsafe detail", nil), code: codes.NotFound},
		{name: "conflict", err: apperr.New(apperr.CodeConflict, "unsafe detail", nil), code: codes.AlreadyExists},
		{name: "rate limited", err: apperr.New(apperr.CodeTooManyRequests, "unsafe detail", nil), code: codes.ResourceExhausted},
		{name: "unexpected", err: errors.New("database password=secret"), code: codes.Internal},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			mapped := commongrpc.StatusError(test.err)
			if got := status.Code(mapped); got != test.code {
				t.Fatalf("code = %s, want %s: %v", got, test.code, mapped)
			}
			if strings.Contains(status.Convert(mapped).Message(), "secret") ||
				strings.Contains(status.Convert(mapped).Message(), "unsafe detail") {
				t.Fatalf("mapped status leaked internal text: %v", mapped)
			}
		})
	}
}

func TestStatusErrorPreservesExplicitStatusAndNil(t *testing.T) {
	t.Parallel()

	if commongrpc.StatusError(nil) != nil {
		t.Fatal("nil error was changed")
	}
	explicit := status.Error(codes.FailedPrecondition, "safe contract message")
	if got := commongrpc.StatusError(explicit); got != explicit {
		t.Fatalf("explicit status changed: %v", got)
	}
}
