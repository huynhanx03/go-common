package cid

import (
	"google.golang.org/grpc"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	grpcinterceptors "github.com/huynhanx03/go-common/pkg/common/grpc/interceptors"
)

// UnaryClientInterceptor delegates to the canonical gRPC transport package.
//
// Deprecated: use interceptors.UnaryCorrelationClient.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return grpcinterceptors.UnaryCorrelationClient()
}

// StreamClientInterceptor delegates to the canonical gRPC transport package.
//
// Deprecated: use interceptors.StreamCorrelationClient.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return grpcinterceptors.StreamCorrelationClient()
}

// UnaryServerInterceptor delegates to the canonical gRPC transport package.
//
// Deprecated: use interceptors.UnaryCorrelationServer.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return grpcinterceptors.UnaryCorrelationServer(
		commongrpc.DefaultCorrelationOptions(),
	)
}

// StreamServerInterceptor delegates to the canonical gRPC transport package.
//
// Deprecated: use interceptors.StreamCorrelationServer.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return grpcinterceptors.StreamCorrelationServer(
		commongrpc.DefaultCorrelationOptions(),
	)
}
