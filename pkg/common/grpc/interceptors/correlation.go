// Package interceptors contains bounded gRPC client and server interceptors.
package interceptors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/correlation"
)

func UnaryCorrelationClient() grpc.UnaryClientInterceptor {
	options, _ := commongrpc.NormalizeCorrelationOptions(commongrpc.CorrelationOptions{})
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
			withOutgoingCorrelation(ctx, options),
			method,
			request,
			reply,
			connection,
			callOptions...,
		)
	}
}

func StreamCorrelationClient() grpc.StreamClientInterceptor {
	options, _ := commongrpc.NormalizeCorrelationOptions(commongrpc.CorrelationOptions{})
	return func(
		ctx context.Context,
		description *grpc.StreamDesc,
		connection *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		callOptions ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(
			withOutgoingCorrelation(ctx, options),
			description,
			connection,
			method,
			callOptions...,
		)
	}
}

func UnaryCorrelationServer(options commongrpc.CorrelationOptions) grpc.UnaryServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeCorrelationOptions(options)
	return func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if configurationError != nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		return handler(withIncomingCorrelation(ctx, normalized), request)
	}
}

func StreamCorrelationServer(options commongrpc.CorrelationOptions) grpc.StreamServerInterceptor {
	normalized, configurationError := commongrpc.NormalizeCorrelationOptions(options)
	return func(
		server any,
		stream grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if configurationError != nil || stream == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		ctx := withIncomingCorrelation(stream.Context(), normalized)
		return handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
	}
}

func withOutgoingCorrelation(
	ctx context.Context,
	options commongrpc.CorrelationOptions,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current, hasMetadata := metadata.FromOutgoingContext(ctx)
	values := current.Get(options.MetadataKey)
	if len(values) == 1 && correlation.Validate(values[0]) == nil {
		return ctx
	}

	id := correlation.FromContext(ctx)
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

func withIncomingCorrelation(
	ctx context.Context,
	options commongrpc.CorrelationOptions,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current, _ := metadata.FromIncomingContext(ctx)
	values := current.Get(options.MetadataKey)
	if len(values) == 1 && correlation.Validate(values[0]) == nil {
		return correlation.WithContext(ctx, values[0])
	}

	id := options.NewID()
	if correlation.Validate(id) != nil {
		id = correlation.New()
	}
	copied := current.Copy()
	copied.Delete(options.MetadataKey)
	copied.Set(options.MetadataKey, id)
	ctx = metadata.NewIncomingContext(ctx, copied)
	return correlation.WithContext(ctx, id)
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *contextServerStream) Context() context.Context {
	return stream.ctx
}
