// Package interceptors provides bounded gRPC transport boundaries.
//
// Server chains should place correlation and logging before recovery so panic
// logs inherit the validated correlation ID and configured logger. Recovery
// then wraps authentication, authorization, and the application handler:
//
//	grpc.ChainUnaryInterceptor(
//		interceptors.UnaryCorrelationServer(grpccommon.DefaultCorrelationOptions()),
//		interceptors.UnaryLoggingServer(log),
//		interceptors.UnaryRecoveryServer(),
//		interceptors.UnaryAuthenticationServer(verifier, source),
//		interceptors.UnaryAuthorizationServer(authorizer, resolve),
//	)
//
// Raw credentials, request messages, response messages, and returned error
// text are never logged by these interceptors.
package interceptors
