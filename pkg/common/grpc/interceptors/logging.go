package interceptors

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func UnaryLoggingServer(base *logger.Logger) grpc.UnaryServerInterceptor {
	base = base.Named("grpc")
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if info == nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		started := time.Now()
		base.Debug(ctx, "rpc started",
			zap.String("method", info.FullMethod),
			zap.Bool("stream", false),
		)
		response, err := handler(ctx, request)
		logRPC(base, ctx, info.FullMethod, false, time.Since(started), err)
		return response, err
	}
}

func StreamLoggingServer(base *logger.Logger) grpc.StreamServerInterceptor {
	base = base.Named("grpc")
	return func(
		server any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if stream == nil || info == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		started := time.Now()
		ctx := stream.Context()
		base.Debug(ctx, "rpc started",
			zap.String("method", info.FullMethod),
			zap.Bool("stream", true),
		)
		err := handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
		logRPC(base, ctx, info.FullMethod, true, time.Since(started), err)
		return err
	}
}

func logRPC(
	log *logger.Logger,
	ctx context.Context,
	method string,
	stream bool,
	duration time.Duration,
	err error,
) {
	code := status.Code(commongrpc.StatusError(err))
	fields := []zap.Field{
		zap.String("method", method),
		zap.String("code", code.String()),
		zap.Duration("duration", duration),
		zap.Bool("stream", stream),
	}
	switch code {
	case codes.OK:
		log.Info(ctx, "rpc", fields...)
	case codes.Canceled, codes.DeadlineExceeded:
		log.Info(ctx, "rpc", fields...)
	case codes.Internal, codes.DataLoss, codes.Unknown:
		log.Error(ctx, "rpc", fields...)
	default:
		log.Warn(ctx, "rpc", fields...)
	}
}
