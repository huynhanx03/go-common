package interceptors

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

func UnaryAuthorizationServer(
	authorizer authorization.Authorizer,
	resolve commongrpc.PermissionResolver,
) grpc.UnaryServerInterceptor {
	configurationError := nilInterface(authorizer) || resolve == nil
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if configurationError || info == nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		if err := authorizeContext(ctx, info.FullMethod, request, authorizer, resolve); err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

func StreamAuthorizationServer(
	authorizer authorization.Authorizer,
	resolve commongrpc.PermissionResolver,
) grpc.StreamServerInterceptor {
	configurationError := nilInterface(authorizer) || resolve == nil
	return func(
		server any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if configurationError || stream == nil || info == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		if err := authorizeContext(stream.Context(), info.FullMethod, nil, authorizer, resolve); err != nil {
			return err
		}
		return handler(server, stream)
	}
}

func authorizeContext(
	ctx context.Context,
	fullMethod string,
	request any,
	authorizer authorization.Authorizer,
	resolve commongrpc.PermissionResolver,
) error {
	if ctx == nil {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	principal, exists := authentication.FromContext(ctx)
	now := time.Now().UTC()
	if !exists || !principal.Authenticated || principal.Validate(now) != nil {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	permission, err := resolve(ctx, fullMethod, request)
	if err != nil {
		if ctx.Err() != nil {
			return commongrpc.StatusError(ctx.Err())
		}
		return status.Error(codes.Internal, "internal server error")
	}
	authorizationRequest := authorization.Request{
		Principal: principal,
		Resource:  permission.Resource,
		Action:    permission.Action,
	}
	if authorizationRequest.Validate(now) != nil {
		return status.Error(codes.Internal, "internal server error")
	}
	decision, err := authorizer.Authorize(ctx, authorizationRequest)
	if err != nil {
		if ctx.Err() != nil {
			return commongrpc.StatusError(ctx.Err())
		}
		return status.Error(codes.Internal, "internal server error")
	}
	if !decision.Allowed {
		return status.Error(codes.PermissionDenied, "permission denied")
	}
	return nil
}
