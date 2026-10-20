package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

// StatusError maps transport-neutral failures to a bounded gRPC status. Raw
// unexpected and application error text is intentionally never returned.
func StatusError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "request canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	}
	if current, ok := status.FromError(err); ok && current.Code() != codes.Unknown {
		return err
	}
	if authenticationFailure(err) {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	if errors.Is(err, authorization.ErrInvalidRequest) {
		return status.Error(codes.InvalidArgument, "invalid authorization request")
	}
	if errors.Is(err, authorization.ErrEvaluation) ||
		errors.Is(err, ErrInvalidConfiguration) {
		return status.Error(codes.Internal, "internal server error")
	}

	var applicationError *apperr.AppError
	if errors.As(err, &applicationError) {
		code, message := applicationStatus(applicationError.Code)
		return status.Error(code, message)
	}
	return status.Error(codes.Internal, "internal server error")
}

func authenticationFailure(err error) bool {
	return errors.Is(err, authentication.ErrInvalidPrincipal) ||
		errors.Is(err, authentication.ErrInvalidPurpose) ||
		errors.Is(err, authentication.ErrInvalidToken) ||
		errors.Is(err, authentication.ErrExpiredToken) ||
		errors.Is(err, authentication.ErrUnknownKey) ||
		errors.Is(err, authentication.ErrRetiredKey) ||
		errors.Is(err, authentication.ErrPurposeMismatch)
}

func applicationStatus(code int) (codes.Code, string) {
	switch {
	case code == apperr.CodeBodyTooLarge || code == apperr.CodeTooManyRequests:
		return codes.ResourceExhausted, "resource exhausted"
	case code == apperr.CodeAccountNotFound:
		return codes.NotFound, "not found"
	case code >= 40000 && code < 41000:
		return codes.InvalidArgument, "invalid argument"
	case code >= 41000 && code < 42000:
		return codes.Unauthenticated, "unauthenticated"
	case code >= 43000 && code < 44000:
		return codes.PermissionDenied, "permission denied"
	case code >= 44000 && code < 45000:
		return codes.NotFound, "not found"
	case code >= 49000 && code < 50000:
		return codes.AlreadyExists, "already exists"
	case code == apperr.CodeGatewayTimeout:
		return codes.DeadlineExceeded, "upstream deadline exceeded"
	default:
		return codes.Internal, "internal server error"
	}
}
