package interceptors

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/logger"
)

const maxStackBytes = 32 << 10

func UnaryRecoveryServer() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (response any, err error) {
		if info == nil || handler == nil {
			return nil, commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		defer func() {
			recovered := recover()
			if recovered == nil {
				err = commongrpc.StatusError(err)
				return
			}
			if ctx != nil && ctx.Err() != nil {
				if !isRecoveredContextTermination(recovered) {
					logRecoveredPanic(ctx, info.FullMethod, false, recovered)
				}
				response = nil
				err = commongrpc.StatusError(ctx.Err())
				return
			}
			logRecoveredPanic(ctx, info.FullMethod, false, recovered)
			response = nil
			err = status.Error(codes.Internal, "internal server error")
		}()
		return handler(ctx, request)
	}
}

func StreamRecoveryServer() grpc.StreamServerInterceptor {
	return func(
		server any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		if stream == nil || info == nil || handler == nil {
			return commongrpc.StatusError(commongrpc.ErrInvalidConfiguration)
		}
		defer func() {
			recovered := recover()
			if recovered == nil {
				err = commongrpc.StatusError(err)
				return
			}
			ctx := stream.Context()
			if ctx != nil && ctx.Err() != nil {
				if !isRecoveredContextTermination(recovered) {
					logRecoveredPanic(ctx, info.FullMethod, true, recovered)
				}
				err = commongrpc.StatusError(ctx.Err())
				return
			}
			logRecoveredPanic(ctx, info.FullMethod, true, recovered)
			err = status.Error(codes.Internal, "internal server error")
		}()
		return handler(server, stream)
	}
}

func isRecoveredContextTermination(recovered any) bool {
	err, ok := recovered.(error)
	return ok && (errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded))
}

func logRecoveredPanic(
	ctx context.Context,
	method string,
	stream bool,
	recovered any,
) {
	stack := debug.Stack()
	if len(stack) > maxStackBytes {
		stack = stack[:maxStackBytes]
	}
	logger.FromContext(ctx).Error(
		"rpc panic recovered",
		zap.String("method", method),
		zap.Bool("stream", stream),
		zap.String("panic_type", fmt.Sprintf("%T", recovered)),
		zap.ByteString("stack", stack),
	)
}
