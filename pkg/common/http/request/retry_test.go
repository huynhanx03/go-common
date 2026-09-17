package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type trackedBody struct {
	reader *strings.Reader
	closed atomic.Bool
}

func newTrackedBody(value string) *trackedBody {
	return &trackedBody{reader: strings.NewReader(value)}
}

func (body *trackedBody) Read(buffer []byte) (int, error) { return body.reader.Read(buffer) }
func (body *trackedBody) Close() error {
	body.closed.Store(true)
	return nil
}

func retryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:       2,
		InitialBackoff:   time.Millisecond,
		MaxBackoff:       10 * time.Millisecond,
		JitterFraction:   0,
		RetryStatusCodes: []int{http.StatusServiceUnavailable},
		RetryMethods:     []string{http.MethodGet},
		Sleep:            func(context.Context, time.Duration) error { return nil },
	}
}

func TestDoZeroRetriesMeansOneAttempt(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	body := newTrackedBody("final")
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test?token=secret", nil)
	response, attempts, err := Do(context.Background(), client, request, RetryPolicy{})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable || calls.Load() != 1 || len(attempts) != 1 {
		t.Fatalf("response=%v calls=%d attempts=%+v", response, calls.Load(), attempts)
	}
	if body.closed.Load() {
		t.Fatal("final response body was closed")
	}
	encoded, _ := json.Marshal(attempts)
	if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte("example.test")) {
		t.Fatalf("attempt metadata leaked URL: %s", encoded)
	}
}

func TestDoRetriesAndClosesIntermediateBody(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	intermediate := newTrackedBody("retry")
	final := newTrackedBody("success")
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       intermediate,
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       final,
			Request:    request,
		}, nil
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
	response, attempts, err := Do(context.Background(), client, request, retryPolicy())
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response == nil || response.StatusCode != http.StatusOK || len(attempts) != 2 {
		t.Fatalf("response=%v attempts=%+v", response, attempts)
	}
	if !intermediate.closed.Load() {
		t.Fatal("intermediate response body was not closed")
	}
	if final.closed.Load() {
		t.Fatal("final response body was closed")
	}
}

func TestDoRejectsNonReplayableAndUnsafeRequests(t *testing.T) {
	t.Parallel()

	policy := retryPolicy()
	policy.RetryMethods = []string{http.MethodPost}
	policy.RequireIdempotencyKeyForUnsafe = true
	var calls atomic.Int32
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       newTrackedBody("retry"),
			Request:    request,
		}, nil
	})}

	missingKey, _ := http.NewRequest(http.MethodPost, "https://example.test", strings.NewReader("payload"))
	response, attempts, err := Do(context.Background(), client, missingKey, policy)
	if response != nil || len(attempts) != 0 || !errors.Is(err, ErrUnsafeRetry) || calls.Load() != 0 {
		t.Fatalf("missing idempotency key response=%v attempts=%+v err=%v calls=%d", response, attempts, err, calls.Load())
	}

	nonReplayable, _ := http.NewRequest(http.MethodPost, "https://example.test", io.NopCloser(strings.NewReader("payload")))
	nonReplayable.Header.Set(IdempotencyKeyHeader, "operation-42")
	response, attempts, err = Do(context.Background(), client, nonReplayable, policy)
	if response == nil || len(attempts) != 1 || !errors.Is(err, ErrBodyNotReplayable) || calls.Load() != 1 {
		t.Fatalf("non-replayable response=%v attempts=%+v err=%v calls=%d", response, attempts, err, calls.Load())
	}
}

func TestDoRetryAfterRespectsDeadline(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	body := newTrackedBody("retry later")
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Retry-After": []string{"10"}},
			Body:       body,
			Request:    request,
		}, nil
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	response, attempts, err := Do(ctx, client, request, retryPolicy())
	if response == nil || len(attempts) != 1 || !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("response=%v attempts=%+v err=%v calls=%d", response, attempts, err, calls.Load())
	}
	if body.closed.Load() {
		t.Fatal("response returned with deadline error was closed")
	}
}

func TestDoCancellationAndFinalTransportError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	transportFailure := errors.New("connection reset")
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, transportFailure
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, attempts, err := Do(ctx, client, request, retryPolicy())
	if response != nil || len(attempts) != 0 || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancelled response=%v attempts=%+v err=%v calls=%d", response, attempts, err, calls.Load())
	}

	policy := retryPolicy()
	policy.RetryError = func(error) bool { return false }
	response, attempts, err = Do(context.Background(), client, request, policy)
	if response != nil || len(attempts) != 1 || !errors.Is(err, transportFailure) {
		t.Fatalf("final error response=%v attempts=%+v err=%v", response, attempts, err)
	}
	if attempts[0].ErrorCode == "" {
		t.Fatal("transport attempt has no stable error code")
	}
}
