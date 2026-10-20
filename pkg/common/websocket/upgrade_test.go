package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderwebsocket "github.com/coder/websocket"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func authenticatedPrincipal(subject string) authentication.Principal {
	return authentication.Principal{
		Authenticated:      true,
		Subject:            subject,
		UserID:             strings.TrimPrefix(subject, "user:"),
		SessionID:          "session-" + subject,
		AuthenticatedUntil: time.Now().Add(time.Hour),
	}
}

func validHubOptions() Options {
	options := DefaultOptions()
	options.AllowedOrigins = []string{"https://app.example.com"}
	return options
}

type runningHubHarness struct {
	hub      *Hub
	server   *httptest.Server
	cancel   context.CancelFunc
	runError <-chan error
}

func startRunningHub(
	t *testing.T,
	options Options,
	resolve PrincipalResolver,
	authorize TopicAuthorizer,
) runningHubHarness {
	t.Helper()

	hub, err := NewHub(options)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- hub.Run(runContext)
	}()
	select {
	case <-hub.started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("hub did not start")
	}
	server := httptest.NewServer(hub.Handler(resolve, authorize))
	harness := runningHubHarness{
		hub:      hub,
		server:   server,
		cancel:   cancel,
		runError: runErrors,
	}
	t.Cleanup(func() {
		server.Close()
		cancel()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = hub.Shutdown(shutdownContext)
		select {
		case <-runErrors:
		case <-time.After(2 * time.Second):
			t.Error("hub Run did not return")
		}
	})
	return harness
}

func dialHub(
	ctx context.Context,
	serverURL string,
	origin string,
	headers http.Header,
) (*coderwebsocket.Conn, *http.Response, error) {
	if headers == nil {
		headers = make(http.Header)
	}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	return coderwebsocket.Dial(
		ctx,
		"ws"+strings.TrimPrefix(serverURL, "http"),
		&coderwebsocket.DialOptions{HTTPHeader: headers},
	)
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before deadline")
}

func TestUpgradeUsesExactOriginAndExplicitPrincipalResolution(t *testing.T) {
	options := validHubOptions()
	var resolverCalls atomic.Int32
	resolve := func(
		context.Context,
		*http.Request,
	) (authentication.Principal, error) {
		resolverCalls.Add(1)
		return authentication.Anonymous(), nil
	}
	harness := startRunningHub(
		t,
		options,
		resolve,
		func(context.Context, authentication.Principal, string) error { return nil },
	)

	for _, origin := range []string{"", "https://evil.example.com", "https://app.example.com.evil.test"} {
		connection, response, err := dialHub(context.Background(), harness.server.URL, origin, nil)
		if connection != nil {
			_ = connection.CloseNow()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q connection=%v status=%v error=%v", origin, connection, responseStatus(response), err)
		}
	}
	if resolverCalls.Load() != 0 {
		t.Fatalf("resolver calls for rejected origins = %d", resolverCalls.Load())
	}

	connection, response, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		nil,
	)
	if err != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("valid origin connection=%v status=%v error=%v", connection, responseStatus(response), err)
	}
	defer connection.CloseNow()
	if resolverCalls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolverCalls.Load())
	}
	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		return len(harness.hub.state.connections) == 1 &&
			len(harness.hub.state.subjects) == 0
	})
}

func TestUpgradeRejectsResolverErrorsAndInvalidPrincipals(t *testing.T) {
	tests := []struct {
		name      string
		principal authentication.Principal
		err       error
	}{
		{name: "present invalid credential", err: errors.New("token=secret")},
		{name: "invalid anonymous identity", principal: authentication.Principal{Subject: "leaked"}},
		{name: "expired authenticated", principal: authentication.Principal{
			Authenticated:      true,
			Subject:            "user:expired",
			AuthenticatedUntil: time.Now().Add(-time.Minute),
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			harness := startRunningHub(
				t,
				validHubOptions(),
				func(context.Context, *http.Request) (authentication.Principal, error) {
					return test.principal, test.err
				},
				func(context.Context, authentication.Principal, string) error { return nil },
			)
			connection, response, err := dialHub(
				context.Background(),
				harness.server.URL,
				"https://app.example.com",
				nil,
			)
			if connection != nil {
				_ = connection.CloseNow()
			}
			if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("connection=%v status=%v error=%v", connection, responseStatus(response), err)
			}
			if strings.Contains(readResponseBody(response), "secret") {
				t.Fatal("upgrade error leaked resolver cause")
			}
		})
	}
}

func TestUpgradeConnectionAndRemoteIPRateLimitsAreBounded(t *testing.T) {
	options := validHubOptions()
	options.MaxConnections = 1
	options.MaxConnectionsPerPrincipal = 1
	options.MaxConnectionsPerRemoteIP = 1
	options.UpgradeRatePerSecondPerRemoteIP = 1
	options.UpgradeBurstPerRemoteIP = 1
	harness := startRunningHub(
		t,
		options,
		func(context.Context, *http.Request) (authentication.Principal, error) {
			return authenticatedPrincipal("user:42"), nil
		},
		func(context.Context, authentication.Principal, string) error { return nil },
	)

	firstHeaders := http.Header{"X-Forwarded-For": []string{"203.0.113.10"}}
	first, _, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		firstHeaders,
	)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	defer first.CloseNow()
	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		return len(harness.hub.state.connections) == 1
	})

	secondHeaders := http.Header{"X-Forwarded-For": []string{"198.51.100.20"}}
	second, response, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		secondHeaders,
	)
	if second != nil {
		_ = second.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second connection=%v status=%v error=%v", second, responseStatus(response), err)
	}
}

func TestRemoteIPTrustsForwardingOnlyFromConfiguredPeers(t *testing.T) {
	t.Parallel()

	untrusted, err := NewHub(validHubOptions())
	if err != nil {
		t.Fatalf("NewHub untrusted: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/ws", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.20")
	if got, err := untrusted.remoteIP(request); err != nil || got != "192.0.2.10" {
		t.Fatalf("untrusted remote IP = %q, %v", got, err)
	}

	options := validHubOptions()
	options.TrustedProxies = []string{"10.0.0.0/8"}
	trusted, err := NewHub(options)
	if err != nil {
		t.Fatalf("NewHub trusted: %v", err)
	}
	request.RemoteAddr = "10.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 10.0.0.2")
	if got, err := trusted.remoteIP(request); err != nil || got != "203.0.113.20" {
		t.Fatalf("trusted remote IP = %q, %v", got, err)
	}
	request.Header.Set("X-Forwarded-For", "malformed")
	if got, err := trusted.remoteIP(request); err != nil || got != "10.0.0.1" {
		t.Fatalf("malformed forwarded remote IP = %q, %v", got, err)
	}
}

func TestUpgradeBeforeRunFailsServiceUnavailable(t *testing.T) {
	t.Parallel()

	hub, err := NewHub(validHubOptions())
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	handler := hub.Handler(
		func(context.Context, *http.Request) (authentication.Principal, error) {
			t.Fatal("resolver must not run before Hub.Run")
			return authentication.Anonymous(), nil
		},
		func(context.Context, authentication.Principal, string) error { return nil },
	)
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	request.Header.Set("Origin", "https://app.example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func readResponseBody(response *http.Response) string {
	if response == nil || response.Body == nil {
		return ""
	}
	buffer := make([]byte, 1024)
	count, _ := response.Body.Read(buffer)
	return string(buffer[:count])
}
