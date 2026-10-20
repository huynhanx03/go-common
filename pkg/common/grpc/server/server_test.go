package server_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	grpcserver "github.com/huynhanx03/go-common/pkg/common/grpc/server"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestServerServesHealthAndStopsOnCancellation(t *testing.T) {
	server, err := grpcserver.New(grpcserver.Config{Name: "test-grpc", Address: "127.0.0.1:0"}, func(*grpc.Server) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for server.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Address() == "" {
		t.Fatal("server did not listen")
	}
	client, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer checkCancel()
	response, err := grpc_health_v1.NewHealthClient(client).Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, %v", response, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop on context cancellation")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("idempotent Shutdown: %v", err)
	}
}

func TestServerRejectsInvalidConfigurationAndOccupiedPort(t *testing.T) {
	for _, config := range []grpcserver.Config{
		{},
		{Name: "test", Address: "bad-address"},
		{Name: "test", Address: "127.0.0.1:0", MaxRecvBytes: -1},
	} {
		if _, err := grpcserver.New(config, func(*grpc.Server) {}); err == nil {
			t.Fatalf("New(%+v) accepted invalid configuration", config)
		}
	}
	if _, err := grpcserver.New(grpcserver.Config{Name: "test", Address: "127.0.0.1:0"}, nil); err == nil {
		t.Fatal("New accepted nil registration")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server, err := grpcserver.New(grpcserver.Config{Name: "test", Address: listener.Addr().String()}, func(*grpc.Server) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run accepted occupied port")
	}
	if err := server.Shutdown(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown after bind failure: %v", err)
	}
}

func TestShutdownForcesAnInFlightRPCAtDeadline(t *testing.T) {
	entered := make(chan struct{})
	server, err := grpcserver.New(grpcserver.Config{Name: "slow-grpc", Address: "127.0.0.1:0"}, func(server *grpc.Server) {
		server.RegisterService(&grpc.ServiceDesc{
			ServiceName: "test.Slow", HandlerType: (*slowService)(nil),
			Methods: []grpc.MethodDesc{{MethodName: "Wait", Handler: func(_ any, ctx context.Context, _ func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			}}},
		}, struct{}{})
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for server.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	client, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- client.Invoke(context.Background(), "/test.Slow/Wait", &emptypb.Empty{}, &emptypb.Empty{})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC did not enter handler")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline", err)
	}
	select {
	case <-rpcDone:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC stayed live after force stop")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run stayed live after force stop")
	}
}

type slowService interface{}

func TestRecoveredPanicUsesServiceLoggerAndCompletesRPCLog(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	server, err := grpcserver.New(grpcserver.Config{
		Name: "panic-grpc", Address: "127.0.0.1:0", Logger: zap.New(core),
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
				panic("test panic")
			},
		},
	}, func(*grpc.Server) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for server.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	client, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	callCtx, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()
	_, err = grpc_health_v1.NewHealthClient(client).Check(callCtx, &grpc_health_v1.HealthCheckRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("health RPC status = %v, want Internal", err)
	}
	if logs.FilterMessage("rpc panic recovered").Len() != 1 || logs.FilterMessage("rpc").Len() != 1 {
		t.Fatalf("service logger entries: panic=%d completion=%d, want 1 each",
			logs.FilterMessage("rpc panic recovered").Len(), logs.FilterMessage("rpc").Len())
	}
}
