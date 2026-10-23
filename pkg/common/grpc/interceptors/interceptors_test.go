package interceptors_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/common/grpc/interceptors"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

const (
	testUnaryMethod  = "/go_common.test.Transport/Unary"
	testStreamMethod = "/go_common.test.Transport/Stream"
)

var fixedNow = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

type verifierFunc func(context.Context, string, authentication.TokenPurpose) (authentication.Principal, error)

func (function verifierFunc) Verify(
	ctx context.Context,
	raw string,
	purpose authentication.TokenPurpose,
) (authentication.Principal, error) {
	return function(ctx, raw, purpose)
}

type transportServer interface {
	Unary(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
	Stream(grpc.ServerStream) error
}

type testTransportServer struct{}

func (testTransportServer) Unary(
	ctx context.Context,
	request *wrapperspb.StringValue,
) (*wrapperspb.StringValue, error) {
	switch request.Value {
	case "panic":
		panic("token=secret")
	case "error":
		return nil, errors.New("database password=secret")
	}
	principal, _ := authentication.FromContext(ctx)
	return wrapperspb.String(principal.Subject + "|" + logger.RequestID(ctx)), nil
}

func (testTransportServer) Stream(stream grpc.ServerStream) error {
	var request wrapperspb.StringValue
	if err := stream.RecvMsg(&request); err != nil {
		return err
	}
	principal, _ := authentication.FromContext(stream.Context())
	return stream.SendMsg(wrapperspb.String(
		principal.Subject + "|" + logger.RequestID(stream.Context()),
	))
}

var transportServiceDescription = grpc.ServiceDesc{
	ServiceName: "go_common.test.Transport",
	HandlerType: (*transportServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Unary",
		Handler: func(
			server any,
			ctx context.Context,
			decode func(any) error,
			interceptor grpc.UnaryServerInterceptor,
		) (any, error) {
			request := new(wrapperspb.StringValue)
			if err := decode(request); err != nil {
				return nil, err
			}
			if interceptor == nil {
				return server.(transportServer).Unary(ctx, request)
			}
			info := &grpc.UnaryServerInfo{Server: server, FullMethod: testUnaryMethod}
			handler := func(ctx context.Context, request any) (any, error) {
				return server.(transportServer).Unary(ctx, request.(*wrapperspb.StringValue))
			}
			return interceptor(ctx, request, info, handler)
		},
	}},
	Streams: []grpc.StreamDesc{{
		StreamName:    "Stream",
		ServerStreams: true,
		ClientStreams: true,
		Handler: func(server any, stream grpc.ServerStream) error {
			return server.(transportServer).Stream(stream)
		},
	}},
}

type harness struct {
	connection *grpc.ClientConn
	logs       *observer.ObservedLogs
	close      func()
}

func newHarness(t *testing.T) harness {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)
	root := zap.New(core)
	source := commongrpc.DefaultBearerMetadataSource()
	source.Optional = true
	source.MaxCredentialBytes = 32
	source.Now = func() time.Time { return fixedNow }
	verifier := verifierFunc(func(
		_ context.Context,
		raw string,
		purpose authentication.TokenPurpose,
	) (authentication.Principal, error) {
		if raw != "good" || purpose != authentication.PurposeAccess {
			return authentication.Anonymous(), authentication.ErrInvalidToken
		}
		return authentication.Principal{
			Authenticated:      true,
			Subject:            "user:42",
			UserID:             "42",
			AuthenticatedUntil: fixedNow.Add(time.Hour),
		}, nil
	})

	commonLogger := logger.Wrap(root)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			interceptors.UnaryRequestIDServer(commongrpc.DefaultRequestIDOptions()),
			interceptors.UnaryLoggingServer(commonLogger),
			interceptors.UnaryRecoveryServer(commonLogger),
			interceptors.UnaryAuthenticationServer(verifier, source),
		),
		grpc.ChainStreamInterceptor(
			interceptors.StreamRequestIDServer(commongrpc.DefaultRequestIDOptions()),
			interceptors.StreamLoggingServer(commonLogger),
			interceptors.StreamRecoveryServer(commonLogger),
			interceptors.StreamAuthenticationServer(verifier, source),
		),
	)
	server.RegisterService(&transportServiceDescription, testTransportServer{})
	listener := bufconn.Listen(1 << 20)
	go func() {
		_ = server.Serve(listener)
	}()

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("NewClient: %v", err)
	}
	return harness{
		connection: connection,
		logs:       logs,
		close: func() {
			_ = connection.Close()
			server.Stop()
			_ = listener.Close()
		},
	}
}

func invokeUnary(
	ctx context.Context,
	connection *grpc.ClientConn,
	value string,
) (*wrapperspb.StringValue, error) {
	response := new(wrapperspb.StringValue)
	err := connection.Invoke(ctx, testUnaryMethod, wrapperspb.String(value), response)
	return response, err
}

func TestBufconnCorrelationAuthenticationLoggingAndRecovery(t *testing.T) {
	harness := newHarness(t)
	defer harness.close()

	t.Run("invalid request ID is dropped and absent optional auth is anonymous", func(t *testing.T) {
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
			strings.ToLower(logger.RequestIDHeader), "invalid request ID",
		))
		response, err := invokeUnary(ctx, harness.connection, "ok")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		parts := strings.Split(response.Value, "|")
		if len(parts) != 2 || parts[0] != "" || parts[1] != "" {
			t.Fatalf("response = %q", response.Value)
		}
	})

	t.Run("valid auth and request ID cross unary boundary", func(t *testing.T) {
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
			strings.ToLower(logger.RequestIDHeader), "upstream-request",
			"authorization", "Bearer good",
		))
		response, err := invokeUnary(ctx, harness.connection, "ok")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if response.Value != "user:42|upstream-request" {
			t.Fatalf("response = %q", response.Value)
		}

		found := false
		for _, entry := range harness.logs.All() {
			fields := entry.ContextMap()
			if entry.Message == "rpc" && fields["request_id"] == "upstream-request" {
				found = true
				if fields["method"] != testUnaryMethod || fields["code"] != codes.OK.String() {
					t.Fatalf("log fields = %+v", fields)
				}
			}
		}
		if !found {
			t.Fatal("no correlation-enriched RPC access log")
		}
	})

	t.Run("present invalid and oversized optional credentials reject", func(t *testing.T) {
		for _, credential := range []string{"Bearer bad", "Bearer " + strings.Repeat("x", 33)} {
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
				"authorization", credential,
			))
			_, err := invokeUnary(ctx, harness.connection, "ok")
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("credential length=%d code=%s error=%v", len(credential), status.Code(err), err)
			}
		}
	})

	t.Run("stream context is wrapped through both interceptors", func(t *testing.T) {
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
			strings.ToLower(logger.RequestIDHeader), "stream-request",
			"authorization", "Bearer good",
		))
		stream, err := harness.connection.NewStream(
			ctx,
			&grpc.StreamDesc{ServerStreams: true, ClientStreams: true},
			testStreamMethod,
		)
		if err != nil {
			t.Fatalf("NewStream: %v", err)
		}
		if err := stream.SendMsg(wrapperspb.String("ok")); err != nil {
			t.Fatalf("SendMsg: %v", err)
		}
		if err := stream.CloseSend(); err != nil {
			t.Fatalf("CloseSend: %v", err)
		}
		response := new(wrapperspb.StringValue)
		if err := stream.RecvMsg(response); err != nil {
			t.Fatalf("RecvMsg: %v", err)
		}
		if response.Value != "user:42|stream-request" {
			t.Fatalf("response = %q", response.Value)
		}
	})

	t.Run("panic and unknown errors are redacted", func(t *testing.T) {
		for _, value := range []string{"panic", "error"} {
			_, err := invokeUnary(context.Background(), harness.connection, value)
			if status.Code(err) != codes.Internal {
				t.Fatalf("%s code=%s error=%v", value, status.Code(err), err)
			}
			if strings.Contains(status.Convert(err).Message(), "secret") {
				t.Fatalf("%s leaked internal error: %v", value, err)
			}
		}
	})
}

func TestCorrelationClientInterceptorsCopyAndValidateMetadata(t *testing.T) {
	t.Parallel()

	original := metadata.Pairs(strings.ToLower(logger.RequestIDHeader), "invalid request ID", "caller", "kept")
	ctx := metadata.NewOutgoingContext(
		logger.WithRequestID(context.Background(), "context-request"),
		original,
	)
	var seen metadata.MD
	err := interceptors.UnaryRequestIDClient()(
		ctx,
		testUnaryMethod,
		nil,
		nil,
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			seen, _ = metadata.FromOutgoingContext(ctx)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if got := seen.Get(strings.ToLower(logger.RequestIDHeader)); len(got) != 1 || got[0] != "context-request" {
		t.Fatalf("seen request ID = %v", got)
	}
	if got := original.Get(strings.ToLower(logger.RequestIDHeader)); len(got) != 1 || got[0] != "invalid request ID" {
		t.Fatalf("caller metadata was mutated: %v", original)
	}
	if got := seen.Get("caller"); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("unrelated metadata lost: %v", seen)
	}

	var streamContext context.Context
	_, err = interceptors.StreamRequestIDClient()(
		ctx,
		&grpc.StreamDesc{},
		nil,
		testStreamMethod,
		func(
			ctx context.Context,
			_ *grpc.StreamDesc,
			_ *grpc.ClientConn,
			_ string,
			_ ...grpc.CallOption,
		) (grpc.ClientStream, error) {
			streamContext = ctx
			return nil, nil
		},
	)
	if err != nil || streamContext == nil {
		t.Fatalf("stream context=%v error=%v", streamContext, err)
	}
}

func TestRequestIDServerDropsDuplicateMetadataFromHandlerContext(t *testing.T) {
	t.Parallel()

	key := strings.ToLower(logger.RequestIDHeader)
	options := commongrpc.DefaultRequestIDOptions()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		key, "first-request",
		key, "second-request",
		"caller", "kept",
	))
	_, err := interceptors.UnaryRequestIDServer(options)(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: testUnaryMethod},
		func(ctx context.Context, _ any) (any, error) {
			if got := logger.RequestID(ctx); got != "" {
				t.Fatalf("context request ID = %q", got)
			}
			incoming, _ := metadata.FromIncomingContext(ctx)
			if got := incoming.Get(key); len(got) != 0 {
				t.Fatalf("incoming request ID = %v", got)
			}
			if got := incoming.Get("caller"); len(got) != 1 || got[0] != "kept" {
				t.Fatalf("unrelated metadata = %v", incoming)
			}
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
}

type authorizerFunc func(context.Context, authorization.Request) (authorization.Decision, error)

func (function authorizerFunc) Authorize(
	ctx context.Context,
	request authorization.Request,
) (authorization.Decision, error) {
	return function(ctx, request)
}

func TestUnaryAuthorizationFailsClosed(t *testing.T) {
	t.Parallel()

	principal := authentication.Principal{
		Authenticated:      true,
		Subject:            "user:42",
		AuthenticatedUntil: time.Now().Add(time.Hour),
	}
	ctx := authentication.WithPrincipal(context.Background(), principal)
	resolver := func(context.Context, string, any) (commongrpc.Permission, error) {
		return commongrpc.Permission{Resource: "document", Action: "update"}, nil
	}
	var handlerCalls atomic.Int32
	handler := func(context.Context, any) (any, error) {
		handlerCalls.Add(1)
		return "ok", nil
	}

	denied := interceptors.UnaryAuthorizationServer(
		authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
			if request.Principal != principal {
				t.Fatalf("principal = %+v", request.Principal)
			}
			return authorization.Decision{Allowed: false}, nil
		}),
		resolver,
	)
	_, err := denied(ctx, nil, &grpc.UnaryServerInfo{FullMethod: testUnaryMethod}, handler)
	if status.Code(err) != codes.PermissionDenied || handlerCalls.Load() != 0 {
		t.Fatalf("denied code=%s calls=%d error=%v", status.Code(err), handlerCalls.Load(), err)
	}

	failed := interceptors.UnaryAuthorizationServer(
		authorizerFunc(func(context.Context, authorization.Request) (authorization.Decision, error) {
			return authorization.Decision{}, errors.New("policy database secret")
		}),
		resolver,
	)
	_, err = failed(ctx, nil, &grpc.UnaryServerInfo{FullMethod: testUnaryMethod}, handler)
	if status.Code(err) != codes.Internal || strings.Contains(status.Convert(err).Message(), "secret") {
		t.Fatalf("failed error = %v", err)
	}

	_, err = denied(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: testUnaryMethod}, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing principal error = %v", err)
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream fakeServerStream) Context() context.Context { return stream.ctx }

func TestStreamAuthorizationAndRecovery(t *testing.T) {
	t.Parallel()

	principal := authentication.Principal{
		Authenticated:      true,
		Subject:            "user:stream",
		AuthenticatedUntil: time.Now().Add(time.Hour),
	}
	stream := fakeServerStream{
		ctx: authentication.WithPrincipal(context.Background(), principal),
	}
	resolve := func(_ context.Context, method string, request any) (commongrpc.Permission, error) {
		if method != testStreamMethod || request != nil {
			t.Fatalf("method=%q request=%v", method, request)
		}
		return commongrpc.Permission{Resource: "document", Action: "watch"}, nil
	}
	var handlerCalls atomic.Int32
	allowed := interceptors.StreamAuthorizationServer(
		authorizerFunc(func(_ context.Context, request authorization.Request) (authorization.Decision, error) {
			if request.Principal != principal {
				t.Fatalf("principal = %+v", request.Principal)
			}
			return authorization.Decision{Allowed: true}, nil
		}),
		resolve,
	)
	err := allowed(
		nil,
		stream,
		&grpc.StreamServerInfo{FullMethod: testStreamMethod},
		func(any, grpc.ServerStream) error {
			handlerCalls.Add(1)
			return nil
		},
	)
	if err != nil || handlerCalls.Load() != 1 {
		t.Fatalf("allowed error=%v calls=%d", err, handlerCalls.Load())
	}

	recovery := interceptors.StreamRecoveryServer(logger.Wrap(zap.NewNop()))
	err = recovery(
		nil,
		stream,
		&grpc.StreamServerInfo{FullMethod: testStreamMethod},
		func(any, grpc.ServerStream) error {
			panic("credential=secret")
		},
	)
	if status.Code(err) != codes.Internal ||
		strings.Contains(status.Convert(err).Message(), "secret") {
		t.Fatalf("recovery error = %v", err)
	}
}

func TestRecoveryPanicTypeLogDoesNotContainValue(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	ctx := logger.WithRequestID(context.Background(), "panic-request")
	_, err := interceptors.UnaryRecoveryServer(logger.Wrap(zap.New(core)))(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: testUnaryMethod},
		func(context.Context, any) (any, error) {
			panic(fmt.Errorf("credential=secret"))
		},
	)
	if status.Code(err) != codes.Internal {
		t.Fatalf("error = %v", err)
	}
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "secret") {
			t.Fatalf("panic value leaked in message: %+v", entry)
		}
		for _, field := range entry.Context {
			if strings.Contains(field.String, "secret") {
				t.Fatalf("panic value leaked in fields: %+v", entry)
			}
		}
	}
}

func TestRecoveryLogsNonCancellationPanicAfterRPCCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(*logger.Logger, context.Context) error
	}{
		{
			name: "unary",
			invoke: func(log *logger.Logger, ctx context.Context) error {
				_, err := interceptors.UnaryRecoveryServer(log)(
					ctx,
					nil,
					&grpc.UnaryServerInfo{FullMethod: testUnaryMethod},
					func(context.Context, any) (any, error) {
						panic("credential=do-not-log")
					},
				)
				return err
			},
		},
		{
			name: "stream",
			invoke: func(log *logger.Logger, ctx context.Context) error {
				return interceptors.StreamRecoveryServer(log)(
					nil,
					fakeServerStream{ctx: ctx},
					&grpc.StreamServerInfo{FullMethod: testStreamMethod},
					func(any, grpc.ServerStream) error {
						panic("credential=do-not-log")
					},
				)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			core, logs := observer.New(zap.DebugLevel)
			ctx, cancel := context.WithCancel(context.Background())
			ctx = logger.WithRequestID(ctx, "cancelled-panic-request")
			cancel()

			err := test.invoke(logger.Wrap(zap.New(core)), ctx)
			if status.Code(err) != codes.Canceled {
				t.Fatalf("recovery error = %v, want cancelled status", err)
			}
			entries := logs.FilterMessage("rpc panic recovered").All()
			if len(entries) != 1 {
				t.Fatalf("non-cancellation panic was not logged exactly once: %+v", logs.All())
			}
			fields := entries[0].ContextMap()
			if fields["request_id"] != "cancelled-panic-request" {
				t.Fatalf("panic log lost correlation ID: %+v", fields)
			}
			for _, entry := range logs.All() {
				if strings.Contains(entry.Message, "do-not-log") {
					t.Fatalf("panic value leaked in message: %+v", entry)
				}
				for _, field := range entry.Context {
					if strings.Contains(field.String, "do-not-log") {
						t.Fatalf("panic value leaked in fields: %+v", entry)
					}
				}
			}
		})
	}
}
