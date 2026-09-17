// Package server owns a bounded gRPC listener and its process lifecycle.
// Service registration remains with the caller; transport policy stays here.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/grpc/interceptors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/stats"
)

const (
	defaultMaxMessageBytes = 4 << 20
	defaultMaxHeaderBytes  = 16 << 10
	defaultMaxStreams      = 1024
	defaultDrainTimeout    = 10 * time.Second
	maximumMessageBytes    = 64 << 20
)

// Config contains transport limits. A nil TLS config is for a trusted local
// listener; use LoadServerMTLS for a network-facing private service.
type Config struct {
	Name                 string
	Address              string
	TLS                  *tls.Config
	Logger               *zap.Logger
	StatsHandler         stats.Handler
	MaxRecvBytes         int
	MaxSendBytes         int
	MaxHeaderBytes       uint32
	MaxConcurrentStreams uint32
	DrainTimeout         time.Duration
	UnaryInterceptors    []grpc.UnaryServerInterceptor
	StreamInterceptors   []grpc.StreamServerInterceptor
	Keepalive            keepalive.ServerParameters
}

// Server implements lifecycle.Component. It starts once and owns one listener.
type Server struct {
	config Config
	grpc   *grpc.Server
	health *health.Server

	mu           sync.Mutex
	listener     net.Listener
	started      bool
	stopped      bool
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
}

// New validates configuration, creates the server and registers all services
// before Run, so registration cannot race with incoming RPCs.
func New(config Config, register func(*grpc.Server)) (*Server, error) {
	if err := validate(config, register); err != nil {
		return nil, err
	}
	if config.MaxRecvBytes == 0 {
		config.MaxRecvBytes = defaultMaxMessageBytes
	}
	if config.MaxSendBytes == 0 {
		config.MaxSendBytes = defaultMaxMessageBytes
	}
	if config.MaxHeaderBytes == 0 {
		config.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if config.MaxConcurrentStreams == 0 {
		config.MaxConcurrentStreams = defaultMaxStreams
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = defaultDrainTimeout
	}
	options := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(config.MaxRecvBytes),
		grpc.MaxSendMsgSize(config.MaxSendBytes),
		grpc.MaxHeaderListSize(config.MaxHeaderBytes),
		grpc.MaxConcurrentStreams(config.MaxConcurrentStreams),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: time.Minute}),
	}
	if config.TLS != nil {
		options = append(options, grpc.Creds(credentials.NewTLS(config.TLS.Clone())))
	}
	if config.StatsHandler != nil {
		options = append(options, grpc.StatsHandler(config.StatsHandler))
	}
	if config.Keepalive != (keepalive.ServerParameters{}) {
		options = append(options, grpc.KeepaliveParams(config.Keepalive))
	}
	unary := make([]grpc.UnaryServerInterceptor, 0, len(config.UnaryInterceptors)+2)
	stream := make([]grpc.StreamServerInterceptor, 0, len(config.StreamInterceptors)+2)
	if config.Logger != nil {
		unary = append(unary, interceptors.UnaryLoggingServer(config.Logger))
		stream = append(stream, interceptors.StreamLoggingServer(config.Logger))
	}
	unary = append(unary, interceptors.UnaryRecoveryServer())
	stream = append(stream, interceptors.StreamRecoveryServer())
	unary = append(unary, config.UnaryInterceptors...)
	stream = append(stream, config.StreamInterceptors...)
	options = append(options, grpc.ChainUnaryInterceptor(unary...), grpc.ChainStreamInterceptor(stream...))
	grpcServer := grpc.NewServer(options...)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	register(grpcServer)
	return &Server{config: config, grpc: grpcServer, health: healthServer, shutdownDone: make(chan struct{})}, nil
}

func validate(config Config, register func(*grpc.Server)) error {
	if config.Name == "" || len(config.Name) > 64 || config.Address == "" || register == nil {
		return fmt.Errorf("grpc server: invalid configuration")
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return fmt.Errorf("grpc server: invalid address: %w", err)
	}
	if config.MaxRecvBytes < 0 || config.MaxRecvBytes > maximumMessageBytes ||
		config.MaxSendBytes < 0 || config.MaxSendBytes > maximumMessageBytes ||
		config.MaxHeaderBytes > 1<<20 || config.MaxConcurrentStreams > 1<<20 ||
		config.DrainTimeout < 0 || config.DrainTimeout > time.Hour {
		return fmt.Errorf("grpc server: invalid limit")
	}
	if config.TLS != nil && config.TLS.MinVersion < tls.VersionTLS12 {
		return fmt.Errorf("grpc server: TLS 1.2 minimum required")
	}
	for _, interceptor := range config.UnaryInterceptors {
		if interceptor == nil {
			return fmt.Errorf("grpc server: nil unary interceptor")
		}
	}
	for _, interceptor := range config.StreamInterceptors {
		if interceptor == nil {
			return fmt.Errorf("grpc server: nil stream interceptor")
		}
	}
	return nil
}

func (server *Server) Name() string { return server.config.Name }

// Address reports the effective listener address after Run has bound it.
func (server *Server) Address() string {
	if server == nil {
		return ""
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return ""
	}
	return server.listener.Addr().String()
}

// Health allows a service to update its own readiness by service name.
func (server *Server) Health() *health.Server { return server.health }

func (server *Server) Run(ctx context.Context) error {
	if server == nil || ctx == nil {
		return fmt.Errorf("grpc server: invalid run context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	server.mu.Lock()
	if server.started || server.stopped {
		server.mu.Unlock()
		return fmt.Errorf("grpc server: already started or stopped")
	}
	server.started = true
	listener, err := net.Listen("tcp", server.config.Address)
	if err != nil {
		server.stopped = true
		server.mu.Unlock()
		return fmt.Errorf("grpc server: listen: %w", err)
	}
	server.listener = listener
	server.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	server.mu.Unlock()

	served := make(chan error, 1)
	go func() { served <- server.grpc.Serve(listener) }()
	select {
	case err = <-served:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			_ = server.Shutdown(context.Background())
			return fmt.Errorf("grpc server: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.config.DrainTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		<-served
		return shutdownErr
	}
}

// Shutdown drains in-flight RPCs within the lesser of the caller's deadline
// and the configured drain timeout, then force-stops remaining RPCs.
func (server *Server) Shutdown(ctx context.Context) error {
	if server == nil || ctx == nil {
		return fmt.Errorf("grpc server: invalid shutdown context")
	}
	server.shutdownOnce.Do(func() {
		defer close(server.shutdownDone)
		server.mu.Lock()
		server.stopped = true
		started := server.listener != nil
		server.mu.Unlock()
		server.health.Shutdown()
		if !started {
			server.grpc.Stop()
			return
		}
		drainCtx, cancel := context.WithTimeout(ctx, server.config.DrainTimeout)
		defer cancel()
		finished := make(chan struct{})
		go func() { server.grpc.GracefulStop(); close(finished) }()
		select {
		case <-finished:
		case <-drainCtx.Done():
			server.grpc.Stop()
			<-finished
			server.shutdownErr = drainCtx.Err()
		}
	})
	<-server.shutdownDone
	return server.shutdownErr
}
