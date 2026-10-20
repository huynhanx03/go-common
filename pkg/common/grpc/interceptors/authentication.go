package interceptors

import (
	"context"
	"reflect"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func UnaryAuthenticationServer(
	verifier authentication.Verifier,
	source commongrpc.MetadataSource,
) grpc.UnaryServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeMetadataSource(source)
	if nilInterface(verifier) {
		configurationError = commongrpc.ErrInvalidConfiguration
	}
	return func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if configurationError != nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		authenticated, err := authenticateContext(ctx, verifier, normalized)
		if err != nil {
			return nil, err
		}
		return handler(authenticated, request)
	}
}

func StreamAuthenticationServer(
	verifier authentication.Verifier,
	source commongrpc.MetadataSource,
) grpc.StreamServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeMetadataSource(source)
	if nilInterface(verifier) {
		configurationError = commongrpc.ErrInvalidConfiguration
	}
	return func(
		server any,
		stream grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if configurationError != nil || stream == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		authenticated, err := authenticateContext(stream.Context(), verifier, normalized)
		if err != nil {
			return err
		}
		return handler(server, &contextServerStream{ServerStream: stream, ctx: authenticated})
	}
}

func authenticateContext(
	ctx context.Context,
	verifier authentication.Verifier,
	source commongrpc.MetadataSource,
) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, present, err := credentialFromMetadata(ctx, source)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated")
	}
	if !present {
		if !source.Optional {
			return nil, status.Error(codes.Unauthenticated, "unauthenticated")
		}
		return authentication.WithPrincipal(ctx, authentication.Anonymous()), nil
	}

	principal, err := verifier.Verify(ctx, raw, authentication.PurposeAccess)
	if err != nil {
		if ctx.Err() != nil {
			return nil, commongrpc.StatusError(ctx.Err())
		}
		return nil, status.Error(codes.Unauthenticated, "unauthenticated")
	}
	if !principal.Authenticated || principal.Validate(source.Now().UTC()) != nil {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated")
	}
	return authentication.WithPrincipal(ctx, principal), nil
}

func credentialFromMetadata(
	ctx context.Context,
	source commongrpc.MetadataSource,
) (string, bool, error) {
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
	if raw == "" ||
		len(raw) > source.MaxCredentialBytes ||
		strings.TrimSpace(raw) != raw {
		return "", true, authentication.ErrInvalidToken
	}
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if character <= 0x20 || character == 0x7f {
			return "", true, authentication.ErrInvalidToken
		}
	}
	return raw, true, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
