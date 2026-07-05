package cid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestNewIsUUIDv7(t *testing.T) {
	id, err := uuid.Parse(New())
	if err != nil {
		t.Fatalf("New() is not a UUID: %v", err)
	}
	if id.Version() != 7 {
		t.Errorf("version = %d, want 7", id.Version())
	}
}

func TestContextRoundTrip(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Errorf("FromContext(empty) = %q, want \"\"", got)
	}

	ctx := WithContext(context.Background(), "abc-123")
	if got := FromContext(ctx); got != "abc-123" {
		t.Errorf("FromContext = %q, want abc-123", got)
	}
}

func TestEnsureContext(t *testing.T) {
	ctx := WithContext(context.Background(), "keep-me")
	if got := FromContext(EnsureContext(ctx)); got != "keep-me" {
		t.Errorf("EnsureContext replaced existing cid with %q", got)
	}

	if got := FromContext(EnsureContext(context.Background())); got == "" {
		t.Error("EnsureContext did not generate a cid")
	}
}

func TestRoundTripperInjectsHeader(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get(Header)
	}))
	defer srv.Close()

	client := &http.Client{Transport: RoundTripper(nil)}

	ctx := WithContext(context.Background(), "cid-outgoing")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := client.Do(req); err != nil {
		t.Fatalf("request: %v", err)
	}

	if received != "cid-outgoing" {
		t.Errorf("server saw header %q, want cid-outgoing", received)
	}
}

func TestRoundTripperRespectsExistingHeader(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get(Header)
	}))
	defer srv.Close()

	client := &http.Client{Transport: RoundTripper(nil)}

	ctx := WithContext(context.Background(), "from-context")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	req.Header.Set(Header, "caller-set")
	if _, err := client.Do(req); err != nil {
		t.Fatalf("request: %v", err)
	}

	if received != "caller-set" {
		t.Errorf("server saw header %q, want caller-set", received)
	}
}
