// Package interceptors provides bounded gRPC transport boundaries.
//
// Server chains should place request ID and logging before recovery so panic
// logs inherit the validated request ID and injected logger. Recovery
// then wraps authentication, authorization, and the application handler:
//
//	grpc.ChainUnaryInterceptor(
//		interceptors.UnaryRequestIDServer(grpccommon.DefaultRequestIDOptions()),
//		interceptors.UnaryLoggingServer(log),
//		interceptors.UnaryRecoveryServer(log),
//		interceptors.UnaryAuthenticationServer(checker, source),
//		interceptors.UnaryAuthorizationServer(checker, resolve),
//	)
//
// Raw credentials, request messages, response messages, and returned error
// text are never logged by these interceptors.
package interceptors
