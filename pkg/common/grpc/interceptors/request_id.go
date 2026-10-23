// Package interceptors contains bounded gRPC client and server interceptors.
package interceptors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func UnaryRequestIDClient() grpc.UnaryClientInterceptor {
	options, _ := commongrpc.NormalizeRequestIDOptions(commongrpc.RequestIDOptions{})
	return func(
		ctx context.Context,
		method string,
		request any,
		reply any,
		connection *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		callOptions ...grpc.CallOption,
	) error {
		return invoker(
			withOutgoingRequestID(ctx, options),
			method,
			request,
			reply,
			connection,
			callOptions...,
		)
	}
}

func StreamRequestIDClient() grpc.StreamClientInterceptor {
	options, _ := commongrpc.NormalizeRequestIDOptions(commongrpc.RequestIDOptions{})
	return func(
		ctx context.Context,
		description *grpc.StreamDesc,
		connection *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		callOptions ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(
			withOutgoingRequestID(ctx, options),
			description,
			connection,
			method,
			callOptions...,
		)
	}
}

func UnaryRequestIDServer(options commongrpc.RequestIDOptions) grpc.UnaryServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeRequestIDOptions(options)
	return func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if configurationError != nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		return handler(withIncomingRequestID(ctx, normalized), request)
	}
}

func StreamRequestIDServer(options commongrpc.RequestIDOptions) grpc.StreamServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeRequestIDOptions(options)
	return func(
		server any,
		stream grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if configurationError != nil || stream == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		ctx := withIncomingRequestID(stream.Context(), normalized)
		return handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
	}
}

func withOutgoingRequestID(
	ctx context.Context,
	options commongrpc.RequestIDOptions,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current, hasMetadata := metadata.FromOutgoingContext(ctx)
	values := current.Get(options.MetadataKey)
	if len(values) == 1 && logger.ValidateRequestID(values[0]) == nil {
		return ctx
	}

	id := logger.RequestID(ctx)
	if !hasMetadata && id == "" {
		return ctx
	}
	copied := current.Copy()
	copied.Delete(options.MetadataKey)
	if id != "" {
		copied.Set(options.MetadataKey, id)
	}
	return metadata.NewOutgoingContext(ctx, copied)
}

func withIncomingRequestID(
	ctx context.Context,
	options commongrpc.RequestIDOptions,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current, _ := metadata.FromIncomingContext(ctx)
	values := current.Get(options.MetadataKey)
	if len(values) == 1 && logger.ValidateRequestID(values[0]) == nil {
		return logger.WithRequestID(ctx, values[0])
	}

	copied := current.Copy()
	copied.Delete(options.MetadataKey)
	return metadata.NewIncomingContext(ctx, copied)
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *contextServerStream) Context() context.Context {
	return stream.ctx
}
