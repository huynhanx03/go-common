package websocket

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

func TestConnectionSelectorValidation(t *testing.T) {
	t.Parallel()

	hub, err := NewHub(validHubOptions())
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	invalidSelectors := []ConnectionSelector{
		{},
		{PrincipalClass: PrincipalClass(99), ConnectionIDs: []string{"connection-1"}},
		{PrincipalClass: PrincipalAnonymous, Subjects: []string{"user:1"}},
		{Subjects: []string{"duplicate", "duplicate"}},
		{ConnectionIDs: []string{strings.Repeat("x", 257)}},
		{Subjects: make([]string, 129)},
	}
	for index, selector := range invalidSelectors {
		if _, err := hub.CloseConnections(
			context.Background(),
			selector,
			DefaultCloseOptions(),
		); !errors.Is(err, ErrInvalidSelector) {
			t.Fatalf("selector[%d] error = %v", index, err)
		}
	}

	for _, closeOptions := range []CloseOptions{
		{},
		{Code: 1005, Reason: "reserved"},
		{Code: CloseNormal, Reason: strings.Repeat("x", 124)},
		{Code: CloseNormal, Reason: "line\nbreak"},
	} {
		if _, err := hub.CloseConnections(
			context.Background(),
			ConnectionSelector{All: true},
			closeOptions,
		); !errors.Is(err, ErrInvalidCloseOptions) {
			t.Fatalf("close options %+v error = %v", closeOptions, err)
		}
	}
}

func TestCloseConnectionsUsesANDGroupsAndIsIdempotent(t *testing.T) {
	harness := startRunningHub(
		t,
		validHubOptions(),
		func(context.Context, *http.Request) (authentication.Principal, error) {
			return authenticatedPrincipal("user:42"), nil
		},
		func(context.Context, authentication.Principal, string) error { return nil },
	)
	connection, _, err := dialHub(
		context.Background(),
		harness.server.URL,
		"https://app.example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer connection.CloseNow()
	sendControlEnvelope(t, connection, OperationSubscribe, "resource:42")

	var connectionID string
	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		if len(harness.hub.state.connections) != 1 ||
			len(harness.hub.state.topics["resource:42"]) != 1 {
			return false
		}
		for id := range harness.hub.state.connections {
			connectionID = id
		}
		return true
	})

	result, err := harness.hub.CloseConnections(
		context.Background(),
		ConnectionSelector{
			PrincipalClass: PrincipalAuthenticated,
			Subjects:       []string{"user:42", "user:other"},
			Topics:         []string{"resource:42"},
		},
		CloseOptions{Code: ClosePolicyViolation, Reason: "policy changed"},
	)
	if err != nil {
		t.Fatalf("CloseConnections: %v", err)
	}
	if result.Matched != 1 || result.MarkedClosing != 1 || result.AlreadyClosing != 0 {
		t.Fatalf("result = %+v", result)
	}

	repeated, err := harness.hub.CloseConnections(
		context.Background(),
		ConnectionSelector{ConnectionIDs: []string{connectionID}},
		CloseOptions{Code: ClosePolicyViolation, Reason: "policy changed"},
	)
	if err != nil {
		t.Fatalf("repeated CloseConnections: %v", err)
	}
	if repeated.Matched != 1 || repeated.MarkedClosing != 0 || repeated.AlreadyClosing != 1 {
		t.Fatalf("repeated result = %+v", repeated)
	}

	waitForCondition(t, func() bool {
		harness.hub.mu.RLock()
		defer harness.hub.mu.RUnlock()
		return len(harness.hub.state.topics["resource:42"]) == 0 &&
			len(harness.hub.state.subjects["user:42"]) == 0
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.Close(CloseNormal, "done"); err != nil && ctx.Err() == nil {
		// The server may already have completed its close handshake.
		_ = err
	}
}
