package cid

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestUnaryClientInterceptorInjectsCID(t *testing.T) {
	var seen string
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		if md, ok := metadata.FromOutgoingContext(ctx); ok {
			if vals := md.Get(Header); len(vals) > 0 {
				seen = vals[0]
			}
		}
		return nil
	}

	ctx := WithContext(context.Background(), "cid-abc")
	_ = UnaryClientInterceptor()(ctx, "/svc/Method", nil, nil, nil, invoker)

	if seen != "cid-abc" {
		t.Errorf("outgoing cid = %q, want cid-abc", seen)
	}
}

func TestUnaryClientInterceptorNoCIDLeavesMetadataUntouched(t *testing.T) {
	var had bool
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		had = ok && len(md.Get(Header)) > 0
		return nil
	}

	_ = UnaryClientInterceptor()(context.Background(), "/svc/Method", nil, nil, nil, invoker)

	if had {
		t.Error("no cid in context must not add a header")
	}
}

func TestUnaryClientInterceptorPreservesCallerHeader(t *testing.T) {
	var seen string
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		seen = md.Get(Header)[0]
		return nil
	}

	ctx := WithContext(context.Background(), "ctx-cid")
	ctx = metadata.AppendToOutgoingContext(ctx, Header, "caller-cid")
	_ = UnaryClientInterceptor()(ctx, "/svc/Method", nil, nil, nil, invoker)

	if seen != "caller-cid" {
		t.Errorf("cid = %q, want caller-cid (explicit header wins)", seen)
	}
}

func TestUnaryServerInterceptorReusesIncomingCID(t *testing.T) {
	var got string
	handler := func(ctx context.Context, req any) (any, error) {
		got = FromContext(ctx)
		return nil, nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(Header, "upstream-cid"))
	_, _ = UnaryServerInterceptor()(ctx, nil, nil, handler)

	if got != "upstream-cid" {
		t.Errorf("handler cid = %q, want upstream-cid", got)
	}
}

func TestUnaryServerInterceptorGeneratesWhenMissing(t *testing.T) {
	var got string
	handler := func(ctx context.Context, req any) (any, error) {
		got = FromContext(ctx)
		return nil, nil
	}

	_, _ = UnaryServerInterceptor()(context.Background(), nil, nil, handler)

	if got == "" {
		t.Error("server must mint a cid when the caller sent none")
	}
}

func TestStreamServerInterceptorPropagatesCID(t *testing.T) {
	var got string
	handler := func(srv any, ss grpc.ServerStream) error {
		got = FromContext(ss.Context())
		return nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(Header, "stream-cid"))
	_ = StreamServerInterceptor()(nil, fakeStream{ctx: ctx}, nil, handler)

	if got != "stream-cid" {
		t.Errorf("stream handler cid = %q, want stream-cid", got)
	}
}

// fakeStream is a minimal grpc.ServerStream exposing only Context.
type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }
