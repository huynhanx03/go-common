package cid

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The gRPC interceptors below carry the correlation ID across a gRPC hop, the
// same way RoundTripper does for outgoing HTTP: the client copies the
// context's cid into the request metadata, and the server reads it back into
// the handler's context. Because logger.FromContext stamps the context's cid
// onto every log line, a server handler needs no extra wiring to have its
// logs correlate — registering UnaryServerInterceptor is enough.
//
// Register on a client:
//
//	grpc.NewClient(target,
//		grpc.WithUnaryInterceptor(cid.UnaryClientInterceptor()),
//		grpc.WithStreamInterceptor(cid.StreamClientInterceptor()))
//
// Register on a server:
//
//	grpc.NewServer(
//		grpc.ChainUnaryInterceptor(cid.UnaryServerInterceptor()),
//		grpc.ChainStreamInterceptor(cid.StreamServerInterceptor()))

// UnaryClientInterceptor copies the context's correlation ID into the
// outgoing metadata of every unary call, so the callee sees the same ID.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(withOutgoingCID(ctx), method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor is UnaryClientInterceptor for streaming calls.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(withOutgoingCID(ctx), desc, cc, method, opts...)
	}
}

// UnaryServerInterceptor reads the correlation ID from the incoming metadata
// (or mints a fresh one) and stores it in the handler's context.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(withServerCID(ctx), req)
	}
}

// StreamServerInterceptor is UnaryServerInterceptor for streaming calls.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, &serverStream{ServerStream: ss, ctx: withServerCID(ss.Context())})
	}
}

// withOutgoingCID appends the context's cid to the outgoing metadata, unless
// the context carries none or the caller already set the header.
func withOutgoingCID(ctx context.Context) context.Context {
	id := FromContext(ctx)
	if id == "" {
		return ctx
	}
	if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(Header)) > 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, Header, id)
}

// withServerCID returns a context carrying the request's correlation ID,
// reused from the incoming metadata or freshly generated.
func withServerCID(ctx context.Context) context.Context {
	id := incomingCID(ctx)
	if id == "" {
		id = New()
	}
	return WithContext(ctx, id)
}

// incomingCID extracts the correlation ID from incoming gRPC metadata.
func incomingCID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if vals := md.Get(Header); len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// serverStream overrides Context so a streaming handler sees the cid-carrying
// context instead of the raw one.
type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }
