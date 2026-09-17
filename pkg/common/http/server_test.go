package http

import (
	"context"
	"errors"
	"net"
	stdhttp "net/http"
	"sync"
	"testing"
	"time"
)

type testAddress string

func (address testAddress) Network() string { return "test" }
func (address testAddress) String() string  { return string(address) }

type blockingListener struct {
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
	err     error
}

func newBlockingListener(err error) *blockingListener {
	return &blockingListener{
		entered: make(chan struct{}),
		closed:  make(chan struct{}),
		err:     err,
	}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.entered) })
	if listener.err != nil {
		return nil, listener.err
	}
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *blockingListener) Close() error {
	select {
	case <-listener.closed:
	default:
		close(listener.closed)
	}
	return nil
}

func (*blockingListener) Addr() net.Addr { return testAddress("test") }

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
	}
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *pipeListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (*pipeListener) Addr() net.Addr { return testAddress("pipe") }

func (listener *pipeListener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case <-listener.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	default:
	}
	select {
	case listener.connections <- server:
		return client, nil
	case <-listener.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func validServerOptions() ServerOptions {
	return ServerOptions{
		Address:           "127.0.0.1:8080",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       3 * time.Second,
		ShutdownTimeout:   time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func TestDefaultServerOptionsAreProductionSafe(t *testing.T) {
	t.Parallel()

	options := DefaultServerOptions("127.0.0.1:8080")
	if err := validateServerOptions(options); err != nil {
		t.Fatalf("default options are invalid: %v", err)
	}
	if options.ReadHeaderTimeout > 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want a bounded slowloris window", options.ReadHeaderTimeout)
	}
	if options.ReadTimeout > options.WriteTimeout {
		t.Fatalf("ReadTimeout = %s exceeds WriteTimeout = %s", options.ReadTimeout, options.WriteTimeout)
	}
	if options.IdleTimeout < options.ReadHeaderTimeout {
		t.Fatalf("IdleTimeout = %s, want at least ReadHeaderTimeout = %s", options.IdleTimeout, options.ReadHeaderTimeout)
	}
	if options.MaxHeaderBytes > 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d, want at most 64 KiB", options.MaxHeaderBytes)
	}
}

func TestServerRunCancellationReturnsBeforeExplicitShutdown(t *testing.T) {
	t.Parallel()

	listener := newBlockingListener(nil)
	server, err := newServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}), validServerOptions(), listener)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = server.Shutdown(cleanupCtx)
	}()
	result := make(chan error, 1)
	go func() { result <- server.Run(ctx) }()
	select {
	case <-listener.entered:
	case <-time.After(time.Second):
		t.Fatal("server did not enter listener Accept")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run after cancellation = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
}

func TestServerRunCancellationLeavesDrainToCallerShutdown(t *testing.T) {
	t.Parallel()

	listener := newPipeListener()

	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var enterOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }

	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if request.URL.Path == "/probe" {
			writer.WriteHeader(stdhttp.StatusNoContent)
			return
		}
		enterOnce.Do(func() { close(requestEntered) })
		<-releaseRequest
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	options := validServerOptions()
	options.ShutdownTimeout = 3 * time.Second
	server, err := newServer(handler, options, listener)
	if err != nil {
		release()
		_ = listener.Close()
		t.Fatalf("newServer: %v", err)
	}

	defer func() {
		release()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = server.Shutdown(cleanupCtx)
		_ = listener.Close()
	}()

	client := &stdhttp.Client{
		Transport: &stdhttp.Transport{Proxy: nil, DialContext: listener.DialContext},
		Timeout:   5 * time.Second,
	}
	defer client.CloseIdleConnections()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(runCtx) }()

	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("http://server.test/block")
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- requestErr
	}()

	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not enter handler")
	}

	cancelRun()
	select {
	case runErr := <-runResult:
		if runErr != nil {
			t.Fatalf("Run after cancellation = %v, want nil", runErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return promptly; cancellation must not wait for graceful drain")
	}

	probeClient := &stdhttp.Client{
		Transport: &stdhttp.Transport{Proxy: nil, DialContext: listener.DialContext},
		Timeout:   500 * time.Millisecond,
	}
	defer probeClient.CloseIdleConnections()
	probe, err := probeClient.Get("http://server.test/probe")
	if err != nil {
		t.Fatalf("listener stopped before explicit Shutdown: %v", err)
	}
	_ = probe.Body.Close()
	if probe.StatusCode != stdhttp.StatusNoContent {
		t.Fatalf("probe status = %d, want %d", probe.StatusCode, stdhttp.StatusNoContent)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 75*time.Millisecond)
	startedShutdown := time.Now()
	shutdownErr := server.Shutdown(shutdownCtx)
	cancelShutdown()
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want caller deadline exceeded", shutdownErr)
	}
	if elapsed := time.Since(startedShutdown); elapsed >= time.Second {
		t.Fatalf("Shutdown elapsed = %s, caller deadline did not outrank configured timeout", elapsed)
	}

	release()
	select {
	case <-requestResult:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not finish after release")
	}
}

func TestServerUnexpectedServeErrorLeavesDrainToExplicitShutdown(t *testing.T) {
	t.Parallel()

	listener := newPipeListener()
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }

	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		close(requestEntered)
		<-releaseRequest
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	server, err := newServer(handler, validServerOptions(), listener)
	if err != nil {
		release()
		_ = listener.Close()
		t.Fatalf("newServer: %v", err)
	}
	defer release()

	shutdownEntered := make(chan struct{})
	server.server.RegisterOnShutdown(func() { close(shutdownEntered) })

	client := &stdhttp.Client{
		Transport: &stdhttp.Transport{Proxy: nil, DialContext: listener.DialContext},
		Timeout:   2 * time.Second,
	}
	defer client.CloseIdleConnections()

	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(context.Background()) }()

	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("http://server.test/block")
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- requestErr
	}()

	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not enter handler")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	select {
	case runErr := <-runResult:
		if !errors.Is(runErr, net.ErrClosed) {
			t.Fatalf("Run error = %v, want listener failure", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return the unexpected listener failure")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- server.Shutdown(shutdownCtx) }()

	select {
	case <-shutdownEntered:
	case err := <-shutdownResult:
		t.Fatalf("Shutdown returned before invoking net/http shutdown: %v", err)
	case <-time.After(time.Second):
		t.Fatal("explicit Shutdown did not invoke net/http shutdown")
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown returned before the active request was released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	release()
	select {
	case shutdownErr := <-shutdownResult:
		if shutdownErr != nil {
			t.Fatalf("Shutdown after request release = %v, want nil", shutdownErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after the active request was released")
	}
	select {
	case requestErr := <-requestResult:
		if requestErr != nil {
			t.Fatalf("request after graceful drain = %v, want nil", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish after graceful drain")
	}
}

func TestServerReturnsUnexpectedServeError(t *testing.T) {
	t.Parallel()

	failure := errors.New("accept failed")
	listener := newBlockingListener(failure)
	server, err := newServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}), validServerOptions(), listener)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = server.Shutdown(cleanupCtx)
	}()
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(context.Background()) }()
	select {
	case runErr := <-runResult:
		if runErr == nil || !stringsContain(runErr.Error(), "accept failed") {
			t.Fatalf("Run error = %v, want accept failure", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return the accept failure")
	}
}

func TestNewServerValidatesOptions(t *testing.T) {
	t.Parallel()

	handler := stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {})
	if _, err := NewServer(nil, validServerOptions()); err == nil {
		t.Fatal("NewServer accepted nil handler")
	}
	tests := []func(*ServerOptions){
		func(options *ServerOptions) { options.Address = "" },
		func(options *ServerOptions) { options.ReadHeaderTimeout = 0 },
		func(options *ServerOptions) { options.ReadTimeout = -time.Second },
		func(options *ServerOptions) { options.WriteTimeout = 0 },
		func(options *ServerOptions) { options.IdleTimeout = 0 },
		func(options *ServerOptions) { options.ShutdownTimeout = 0 },
		func(options *ServerOptions) { options.MaxHeaderBytes = 0 },
	}
	for _, mutate := range tests {
		options := validServerOptions()
		mutate(&options)
		if _, err := NewServer(handler, options); err == nil {
			t.Fatalf("NewServer accepted invalid options: %+v", options)
		}
	}
}

func stringsContain(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
