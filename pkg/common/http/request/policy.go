package request

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxRetries       = 10
	maxBackoff       = time.Minute
	maxPolicyEntries = 32
)

// RetryPolicy is an explicit retry budget. MaxRetries counts attempts after
// the initial request; zero means no retry.
type RetryPolicy struct {
	MaxRetries       int
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	JitterFraction   float64
	RetryStatusCodes []int
	RetryMethods     []string

	// Unsafe methods may appear in RetryMethods only when this is true, and
	// each request must then carry a valid Idempotency-Key.
	RequireIdempotencyKeyForUnsafe bool

	// RetryError optionally classifies transport errors. The default retries
	// only temporary/timeout network errors.
	RetryError func(error) bool

	// Clock/sleep/entropy injection for deterministic tests.
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
	Random func() float64
}

type normalizedRetryPolicy struct {
	RetryPolicy
	statusCodes map[int]struct{}
	methods     map[string]struct{}
}

func normalizeRetryPolicy(input RetryPolicy) (normalizedRetryPolicy, error) {
	if input.MaxRetries < 0 || input.MaxRetries > maxRetries ||
		input.InitialBackoff < 0 ||
		input.MaxBackoff < 0 ||
		input.JitterFraction < 0 ||
		input.JitterFraction > 1 ||
		len(input.RetryStatusCodes) > maxPolicyEntries ||
		len(input.RetryMethods) > maxPolicyEntries {
		return normalizedRetryPolicy{}, ErrInvalidPolicy
	}
	if input.MaxRetries > 0 {
		if input.InitialBackoff <= 0 ||
			input.MaxBackoff < input.InitialBackoff ||
			input.MaxBackoff > maxBackoff {
			return normalizedRetryPolicy{}, ErrInvalidPolicy
		}
	}
	if input.Now == nil {
		input.Now = time.Now
	}
	if input.Sleep == nil {
		input.Sleep = sleepContext
	}
	if input.Random == nil {
		input.Random = rand.Float64
	}
	if input.RetryError == nil {
		input.RetryError = retryableNetworkError
	}

	statusCodes := make(map[int]struct{}, len(input.RetryStatusCodes))
	for _, status := range append([]int(nil), input.RetryStatusCodes...) {
		if status < 100 || status > 599 {
			return normalizedRetryPolicy{}, ErrInvalidPolicy
		}
		statusCodes[status] = struct{}{}
	}

	methodInput := append([]string(nil), input.RetryMethods...)
	if len(methodInput) == 0 {
		methodInput = []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPut,
			http.MethodDelete,
			http.MethodOptions,
			http.MethodTrace,
		}
	}
	methods := make(map[string]struct{}, len(methodInput))
	for _, method := range methodInput {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !validMethod(method) {
			return normalizedRetryPolicy{}, ErrInvalidPolicy
		}
		if !idempotentMethod(method) && !input.RequireIdempotencyKeyForUnsafe {
			return normalizedRetryPolicy{}, ErrInvalidPolicy
		}
		methods[method] = struct{}{}
	}

	input.RetryStatusCodes = append([]int(nil), input.RetryStatusCodes...)
	input.RetryMethods = append([]string(nil), methodInput...)
	return normalizedRetryPolicy{
		RetryPolicy: input,
		statusCodes: statusCodes,
		methods:     methods,
	}, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryableNetworkError(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkError net.Error
	return errors.As(err, &networkError) &&
		(networkError.Timeout() || networkError.Temporary())
}

func idempotentMethod(method string) bool {
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPut,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

func validMethod(method string) bool {
	if method == "" || len(method) > 32 {
		return false
	}
	for index := 0; index < len(method); index++ {
		character := method[index]
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}
