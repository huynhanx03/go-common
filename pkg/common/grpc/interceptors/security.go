package interceptors

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/security"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func UnaryAuthenticationServer(checker *security.Checker, source commongrpc.MetadataSource) grpc.UnaryServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeMetadataSource(source)
	return func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if configurationError != nil || checker == nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		authenticated, err := authenticateContext(ctx, checker, normalized)
		if err != nil {
			return nil, err
		}
		return handler(authenticated, request)
	}
}

func StreamAuthenticationServer(checker *security.Checker, source commongrpc.MetadataSource) grpc.StreamServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeMetadataSource(source)
	return func(server any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if configurationError != nil || checker == nil || stream == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		authenticated, err := authenticateContext(stream.Context(), checker, normalized)
		if err != nil {
			return err
		}
		return handler(server, &contextServerStream{ServerStream: stream, ctx: authenticated})
	}
}

func UnaryAuthorizationServer(checker *security.Checker, resolve commongrpc.PermissionResolver) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if checker == nil || resolve == nil || info == nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		if err := authorizeContext(ctx, info.FullMethod, request, checker, resolve); err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

func StreamAuthorizationServer(checker *security.Checker, resolve commongrpc.PermissionResolver) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if checker == nil || resolve == nil || stream == nil || info == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		if err := authorizeContext(stream.Context(), info.FullMethod, nil, checker, resolve); err != nil {
			return err
		}
		return handler(server, stream)
	}
}

func authenticateContext(ctx context.Context, checker *security.Checker, source commongrpc.MetadataSource) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, present, err := credentialFromMetadata(ctx, source)
	if err != nil {
		return nil, commongrpc.StatusError(security.ErrUnauthenticated)
	}
	principal, err := checker.Authenticate(ctx, raw, authentication.PurposeAccess)
	if err != nil {
		if !source.Optional || present || !errors.Is(err, security.ErrUnauthenticated) {
			return nil, commongrpc.StatusError(err)
		}
		principal = authentication.Anonymous()
	}
	return authentication.WithPrincipal(ctx, principal), nil
}

func authorizeContext(ctx context.Context, method string, request any, checker *security.Checker, resolve commongrpc.PermissionResolver) error {
	principal, err := checker.Principal(ctx)
	if err != nil {
		return commongrpc.StatusError(err)
	}
	permission, err := resolve(ctx, method, request)
	if err != nil {
		if ctx.Err() != nil {
			return commongrpc.StatusError(ctx.Err())
		}
		return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
	}
	_, err = checker.Authorize(ctx, principal, permission)
	return commongrpc.StatusError(err)
}

func credentialFromMetadata(ctx context.Context, source commongrpc.MetadataSource) (string, bool, error) {
	values := metadata.ValueFromIncomingContext(ctx, source.MetadataKey)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, authentication.ErrInvalidToken
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], source.Scheme) {
		return "", true, authentication.ErrInvalidToken
	}
	raw := parts[1]
	if len(raw) > source.MaxCredentialBytes {
		return "", true, authentication.ErrInvalidToken
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] <= 0x20 || raw[index] == 0x7f {
			return "", true, authentication.ErrInvalidToken
		}
	}
	return raw, true, nil
}
