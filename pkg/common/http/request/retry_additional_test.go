package request

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

type temporaryNetworkError struct{}

func (temporaryNetworkError) Error() string   { return "temporary network failure" }
func (temporaryNetworkError) Timeout() bool   { return false }
func (temporaryNetworkError) Temporary() bool { return true }

func TestDoReplaysBodyAndRetriesDefaultNetworkError(t *testing.T) {
	t.Parallel()

	t.Run("replayable unsafe body", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		var bodies []string
		client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			bodies = append(bodies, string(body))
			status := http.StatusServiceUnavailable
			if calls.Add(1) == 3 {
				status = http.StatusCreated
			}
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    request,
			}, nil
		})}
		request, _ := http.NewRequest(http.MethodPost, "https://example.test", strings.NewReader("document"))
		request.Header.Set(IdempotencyKeyHeader, "operation-42")
		policy := retryPolicy()
		policy.RetryMethods = []string{http.MethodPost}
		policy.RequireIdempotencyKeyForUnsafe = true

		response, attempts, err := Do(context.Background(), client, request, policy)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if response == nil || response.StatusCode != http.StatusCreated || len(attempts) != 3 {
			t.Fatalf("response=%#v attempts=%+v", response, attempts)
		}
		for index, body := range bodies {
			if body != "document" {
				t.Fatalf("body[%d] = %q", index, body)
			}
		}
	})

	t.Run("temporary network error", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return nil, temporaryNetworkError{}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    request,
			}, nil
		})}
		request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)

		response, attempts, err := Do(context.Background(), client, request, retryPolicy())
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if response == nil || response.StatusCode != http.StatusOK || len(attempts) != 2 {
			t.Fatalf("response=%#v attempts=%+v", response, attempts)
		}
	})
}

func TestDoCancellationDuringBackoffAndImpossibleClientResult(t *testing.T) {
	t.Parallel()

	t.Run("cancel during sleep", func(t *testing.T) {
		t.Parallel()

		intermediate := newTrackedBody("retry")
		client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       intermediate,
				Request:    request,
			}, nil
		})}
		request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		policy := retryPolicy()
		policy.Sleep = func(context.Context, time.Duration) error {
			return context.Canceled
		}

		response, attempts, err := Do(context.Background(), client, request, policy)
		if response != nil || len(attempts) != 1 || !errors.Is(err, context.Canceled) {
			t.Fatalf("response=%#v attempts=%+v err=%v", response, attempts, err)
		}
		if !intermediate.closed.Load() {
			t.Fatal("intermediate response was not closed before sleeping")
		}
	})

	t.Run("round tripper nil result becomes an error", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return nil, nil
		})}
		request, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		policy := retryPolicy()
		policy.RetryError = func(error) bool { return false }

		response, attempts, err := Do(context.Background(), client, request, policy)
		if response != nil || len(attempts) != 1 || err == nil {
			t.Fatalf("response=%#v attempts=%+v err=%v", response, attempts, err)
		}
	})
}

func TestDoRejectsInvalidInputsAndGetBodyFailure(t *testing.T) {
	t.Parallel()

	validRequest, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
	validClient := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})}
	for name, test := range map[string]struct {
		ctx     context.Context
		client  *http.Client
		request *http.Request
	}{
		"nil context": {ctx: nil, client: validClient, request: validRequest},
		"nil client":  {ctx: context.Background(), client: nil, request: validRequest},
		"nil request": {ctx: context.Background(), client: validClient, request: nil},
		"nil URL": {
			ctx:     context.Background(),
			client:  validClient,
			request: &http.Request{Method: http.MethodGet, Header: make(http.Header)},
		},
		"invalid method": {
			ctx:    context.Background(),
			client: validClient,
			request: &http.Request{
				Method: "BAD METHOD",
				URL:    validRequest.URL,
				Header: make(http.Header),
			},
		},
	} {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			response, attempts, err := Do(test.ctx, test.client, test.request, RetryPolicy{})
			if response != nil || attempts != nil || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("response=%#v attempts=%+v error=%v", response, attempts, err)
			}
		})
	}

	bodyFailure := errors.New("cannot clone body")
	request, _ := http.NewRequest(http.MethodPut, "https://example.test", strings.NewReader("payload"))
	request.GetBody = func() (io.ReadCloser, error) {
		return nil, bodyFailure
	}
	response, attempts, err := Do(context.Background(), validClient, request, retryPolicy())
	if response != nil || len(attempts) != 0 ||
		!errors.Is(err, ErrBodyNotReplayable) ||
		!errors.Is(err, bodyFailure) {
		t.Fatalf("response=%#v attempts=%+v error=%v", response, attempts, err)
	}
}

func TestRetryPolicyValidationAndHelpers(t *testing.T) {
	t.Parallel()

	invalidPolicies := []RetryPolicy{
		{MaxRetries: -1},
		{MaxRetries: maxRetries + 1},
		{MaxRetries: 1, InitialBackoff: 0, MaxBackoff: time.Second},
		{MaxRetries: 1, InitialBackoff: time.Second, MaxBackoff: time.Millisecond},
		{MaxRetries: 1, InitialBackoff: time.Second, MaxBackoff: maxBackoff + time.Second},
		{JitterFraction: -0.1},
		{JitterFraction: 1.1},
		{RetryStatusCodes: []int{99}},
		{RetryStatusCodes: []int{600}},
		{RetryMethods: []string{"GET SPACE"}},
		{RetryMethods: []string{http.MethodPost}},
	}
	for index, policy := range invalidPolicies {
		if _, err := normalizeRetryPolicy(policy); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("policy[%d] error = %v", index, err)
		}
	}
	tooManyStatuses := RetryPolicy{RetryStatusCodes: make([]int, maxPolicyEntries+1)}
	if _, err := normalizeRetryPolicy(tooManyStatuses); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("too many statuses error = %v", err)
	}
	tooManyMethods := RetryPolicy{RetryMethods: make([]string, maxPolicyEntries+1)}
	if _, err := normalizeRetryPolicy(tooManyMethods); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("too many methods error = %v", err)
	}

	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "valid-key_42:part", valid: true},
		{value: strings.Repeat("a", maxIdempotencyBytes), valid: true},
		{value: "", valid: false},
		{value: "contains space", valid: false},
		{value: strings.Repeat("a", maxIdempotencyBytes+1), valid: false},
	} {
		if got := validIdempotencyKey(test.value); got != test.valid {
			t.Fatalf("validIdempotencyKey(%q) = %t", test.value, got)
		}
	}
}

func TestRetryDelayRetryAfterAndErrorCodes(t *testing.T) {
	t.Parallel()

	base := normalizedRetryPolicy{RetryPolicy: RetryPolicy{
		InitialBackoff: time.Second,
		MaxBackoff:     4 * time.Second,
		JitterFraction: 0.5,
		Random:         func() float64 { return 0 },
	}}
	if got := retryDelay(base, 0); got != 500*time.Millisecond {
		t.Fatalf("low jitter delay = %v", got)
	}
	base.Random = func() float64 { return 1 }
	if got := retryDelay(base, 10); got != 4*time.Second {
		t.Fatalf("capped high jitter delay = %v", got)
	}
	base.Random = func() float64 { return math.NaN() }
	if got := retryDelay(base, 1); got != 2*time.Second {
		t.Fatalf("NaN random delay = %v", got)
	}

	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); !ok || delay != 5*time.Second {
		t.Fatalf("HTTP-date Retry-After = %v, %t", delay, ok)
	}
	if delay, ok := parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now); !ok || delay != 0 {
		t.Fatalf("past Retry-After = %v, %t", delay, ok)
	}
	for _, value := range []string{
		"-1",
		"not-a-date",
		strings.Repeat("1", 129),
		"86401",
		now.Add(maxRetryAfter + time.Second).Format(http.TimeFormat),
	} {
		if _, ok := parseRetryAfter(value, now); ok {
			t.Fatalf("parseRetryAfter(%q) unexpectedly succeeded", value)
		}
	}

	if got := attemptErrorCode(context.Canceled); got != "context_canceled" {
		t.Fatalf("canceled code = %q", got)
	}
	if got := attemptErrorCode(context.DeadlineExceeded); got != "deadline_exceeded" {
		t.Fatalf("deadline code = %q", got)
	}
	if got := attemptErrorCode(nil); got != "" {
		t.Fatalf("nil code = %q", got)
	}

	drainAndClose(nil)
	if shouldRetry(nil, nil, normalizedRetryPolicy{}) {
		t.Fatal("nil response and error must not be retryable")
	}
	if retryableNetworkError(nil) ||
		retryableNetworkError(context.Canceled) ||
		retryableNetworkError(context.DeadlineExceeded) ||
		retryableNetworkError(errors.New("plain error")) {
		t.Fatal("non-transient errors must not be retried")
	}
}

func TestLegacyRetryCompatibility(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	result := Retry(context.Background(), RetryConfig{
		MaxRetries: 2,
		Backoff:    algorithm.NewConstantBackoff(time.Nanosecond),
	}, func(context.Context) (string, error) {
		if calls.Add(1) < 3 {
			return "", errors.New("retry")
		}
		return "ok", nil
	})
	if result.Err != nil || result.Value != "ok" || result.Attempts != 3 {
		t.Fatalf("result = %+v", result)
	}

	sentinel := errors.New("stop")
	err := RetryVoid(context.Background(), RetryConfig{
		MaxRetries: 2,
		Backoff:    algorithm.NewConstantBackoff(time.Nanosecond),
		ShouldRetry: func(error) bool {
			return false
		},
	}, func(context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RetryVoid error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledResult := Retry(cancelled, RetryConfig{}, func(context.Context) (int, error) {
		return 0, nil
	})
	if !errors.Is(cancelledResult.Err, context.Canceled) || cancelledResult.Attempts != 0 {
		t.Fatalf("cancelled result = %+v", cancelledResult)
	}

	invalid := Retry[int](nil, RetryConfig{}, nil)
	if !errors.Is(invalid.Err, ErrInvalidPolicy) {
		t.Fatalf("invalid result = %+v", invalid)
	}
}
