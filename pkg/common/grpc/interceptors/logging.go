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

func UnaryLoggingServer(root *zap.Logger) grpc.UnaryServerInterceptor {
	base := grpcLogger(root)
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
		ctx = logger.WithContext(ctx, base)
		scoped := logger.FromContext(ctx)
		scoped.Debug("rpc started",
			zap.String("method", info.FullMethod),
			zap.Bool("stream", false),
		)
		response, err := handler(ctx, request)
		logRPC(scoped, info.FullMethod, false, time.Since(started), err)
		return response, err
	}
}

func StreamLoggingServer(root *zap.Logger) grpc.StreamServerInterceptor {
	base := grpcLogger(root)
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
		ctx := logger.WithContext(stream.Context(), base)
		scoped := logger.FromContext(ctx)
		scoped.Debug("rpc started",
			zap.String("method", info.FullMethod),
			zap.Bool("stream", true),
		)
		err := handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
		logRPC(scoped, info.FullMethod, true, time.Since(started), err)
		return err
	}
}

func grpcLogger(root *zap.Logger) *zap.Logger {
	if root == nil {
		root = zap.L()
	}
	return root.Named("grpc")
}

func logRPC(
	log *zap.Logger,
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
		log.Info("rpc", fields...)
	case codes.Canceled, codes.DeadlineExceeded:
		log.Info("rpc", fields...)
	case codes.Internal, codes.DataLoss, codes.Unknown:
		log.Error("rpc", fields...)
	default:
		log.Warn("rpc", fields...)
	}
}
