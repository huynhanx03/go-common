package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"reflect"
	"strconv"
	"sync"
	"time"
)

const (
	minHeaderBytes = 1 << 10
	maxHeaderBytes = 1 << 20

	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultShutdownTimeout   = 30 * time.Second
	defaultMaxHeaderBytes    = 32 << 10
)

var (
	ErrInvalidServerOptions = errors.New("http server: invalid options")
	ErrInvalidServerState   = errors.New("http server: invalid state")
)

type ServerOptions struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

// DefaultServerOptions returns conservative transport bounds suitable for a
// conventional JSON HTTP service. Callers may copy and override individual
// fields before passing the options to NewServer.
func DefaultServerOptions(address string) ServerOptions {
	return ServerOptions{
		Address:           address,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
}

type serverState uint8

const (
	serverNew serverState = iota
	serverRunning
	serverStopping
	serverStopped
)

// Server is a lifecycle-compatible, signal-agnostic HTTP server.
type Server struct {
	mu           sync.Mutex
	server       *stdhttp.Server
	options      ServerOptions
	listener     net.Listener
	state        serverState
	shutdownDone chan struct{}
	shutdownErr  error
}

func NewServer(handler stdhttp.Handler, options ServerOptions) (*Server, error) {
	return newServer(handler, options, nil)
}

func newServer(handler stdhttp.Handler, options ServerOptions, listener net.Listener) (*Server, error) {
	if isNilHandler(handler) || validateServerOptions(options) != nil {
		return nil, ErrInvalidServerOptions
	}
	return &Server{
		server: &stdhttp.Server{
			Addr:              options.Address,
			Handler:           handler,
			ReadHeaderTimeout: options.ReadHeaderTimeout,
			ReadTimeout:       options.ReadTimeout,
			WriteTimeout:      options.WriteTimeout,
			IdleTimeout:       options.IdleTimeout,
			MaxHeaderBytes:    options.MaxHeaderBytes,
		},
		options:      options,
		listener:     listener,
		state:        serverNew,
		shutdownDone: make(chan struct{}),
	}, nil
}

// Name satisfies lifecycle.Component.
func (*Server) Name() string { return "http" }

// Run serves until cancellation, explicit Shutdown, or an unexpected listener
// error. Cancellation only releases the run loop; graceful draining belongs to
// Shutdown so the lifecycle caller remains the sole owner of its deadline.
func (server *Server) Run(ctx context.Context) error {
	if server == nil {
		return ErrInvalidServerState
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	server.mu.Lock()
	if server.state != serverNew {
		server.mu.Unlock()
		return ErrInvalidServerState
	}
	server.state = serverRunning
	server.mu.Unlock()

	serveResult := make(chan error, 1)
	go func() {
		if server.listener != nil {
			serveResult <- server.server.Serve(server.listener)
			return
		}
		serveResult <- server.server.ListenAndServe()
	}()

	select {
	case err := <-serveResult:
		shutting, done := server.markServeReturned()
		if shutting {
			<-done
			server.mu.Lock()
			shutdownErr := server.shutdownErr
			server.mu.Unlock()
			if errors.Is(err, stdhttp.ErrServerClosed) {
				err = nil
			}
			return errors.Join(shutdownErr, err)
		}
		return err
	case <-ctx.Done():
		return nil
	}
}

// Shutdown gracefully drains once using the smaller of the caller deadline and
// the configured shutdown timeout. On timeout it force-closes remaining
// connections so lifecycle progress remains bounded.
func (server *Server) Shutdown(ctx context.Context) error {
	if server == nil {
		return ErrInvalidServerState
	}
	if ctx == nil {
		ctx = context.Background()
	}

	server.mu.Lock()
	switch server.state {
	case serverStopped:
		err := server.shutdownErr
		server.mu.Unlock()
		return err
	case serverStopping:
		done := server.shutdownDone
		server.mu.Unlock()
		select {
		case <-done:
			server.mu.Lock()
			err := server.shutdownErr
			server.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	case serverNew, serverRunning:
		server.state = serverStopping
	default:
		server.mu.Unlock()
		return ErrInvalidServerState
	}
	server.mu.Unlock()

	shutdownContext, cancel := context.WithTimeout(ctx, server.options.ShutdownTimeout)
	err := server.server.Shutdown(shutdownContext)
	cancel()
	if errors.Is(err, stdhttp.ErrServerClosed) {
		err = nil
	}
	if err != nil {
		closeErr := server.server.Close()
		if errors.Is(closeErr, stdhttp.ErrServerClosed) {
			closeErr = nil
		}
		err = errors.Join(err, closeErr)
	}

	server.mu.Lock()
	server.shutdownErr = err
	server.state = serverStopped
	close(server.shutdownDone)
	server.mu.Unlock()
	return err
}

func (server *Server) markServeReturned() (bool, <-chan struct{}) {
	server.mu.Lock()
	defer server.mu.Unlock()
	switch server.state {
	case serverStopping, serverStopped:
		return true, server.shutdownDone
	case serverRunning:
		return false, server.shutdownDone
	default:
		return false, server.shutdownDone
	}
}

func validateServerOptions(options ServerOptions) error {
	if options.Address == "" ||
		options.ReadHeaderTimeout <= 0 ||
		options.ReadTimeout <= 0 ||
		options.WriteTimeout <= 0 ||
		options.IdleTimeout <= 0 ||
		options.ShutdownTimeout <= 0 ||
		options.MaxHeaderBytes < minHeaderBytes ||
		options.MaxHeaderBytes > maxHeaderBytes {
		return ErrInvalidServerOptions
	}
	_, port, err := net.SplitHostPort(options.Address)
	if err != nil || port == "" {
		return fmt.Errorf("%w: address", ErrInvalidServerOptions)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number > 65535 {
		return fmt.Errorf("%w: port", ErrInvalidServerOptions)
	}
	return nil
}

func isNilHandler(handler stdhttp.Handler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
