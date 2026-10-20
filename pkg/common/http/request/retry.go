package request

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
)

const (
	IdempotencyKeyHeader = "Idempotency-Key"
	maxIdempotencyBytes  = 128
	maxDrainBytes        = 64 << 10
	maxRetryAfter        = 24 * time.Hour
)

type Attempt struct {
	Number     int           `json:"number"`
	Duration   time.Duration `json:"duration"`
	StatusCode int           `json:"status_code,omitempty"`
	ErrorCode  string        `json:"error_code,omitempty"`
}

// Do executes an HTTP request within an explicit retry policy.
func Do(
	ctx context.Context,
	client *http.Client,
	request *http.Request,
	policy RetryPolicy,
) (*http.Response, []Attempt, error) {
	if ctx == nil || request == nil || request.URL == nil || client == nil {
		return nil, nil, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	normalized, err := normalizeRetryPolicy(policy)
	if err != nil {
		return nil, nil, err
	}

	method := strings.ToUpper(request.Method)
	if !validMethod(method) {
		return nil, nil, ErrInvalidRequest
	}
	_, methodRetryable := normalized.methods[method]
	if methodRetryable && !idempotentMethod(method) {
		if !normalized.RequireIdempotencyKeyForUnsafe ||
			!validIdempotencyKey(request.Header.Get(IdempotencyKeyHeader)) {
			return nil, nil, ErrUnsafeRetry
		}
	}

	maxAttempts := 1
	if methodRetryable {
		maxAttempts += normalized.MaxRetries
	}
	attempts := make([]Attempt, 0, maxAttempts)
	bodyPresent := request.Body != nil && request.Body != http.NoBody

	for number := 1; number <= maxAttempts; number++ {
		if err := ctx.Err(); err != nil {
			return nil, attempts, err
		}
		outbound, err := cloneAttemptRequest(request, ctx, number)
		if err != nil {
			return nil, attempts, err
		}

		started := normalized.Now()
		response, requestErr := client.Do(outbound)
		duration := normalized.Now().Sub(started)
		if duration < 0 {
			duration = 0
		}
		attempt := Attempt{
			Number:    number,
			Duration:  duration,
			ErrorCode: attemptErrorCode(requestErr),
		}
		if response != nil {
			attempt.StatusCode = response.StatusCode
		}
		attempts = append(attempts, attempt)

		if err := ctx.Err(); err != nil {
			return response, attempts, err
		}
		retryable := number < maxAttempts && shouldRetry(response, requestErr, normalized)
		if !retryable {
			if response == nil && requestErr == nil {
				return nil, attempts, ErrNoResult
			}
			return response, attempts, requestErr
		}
		if bodyPresent && request.GetBody == nil {
			return response, attempts, errors.Join(ErrBodyNotReplayable, requestErr)
		}

		delay := retryDelay(normalized, number-1)
		if response != nil {
			if retryAfter, ok := parseRetryAfter(
				response.Header.Get("Retry-After"),
				normalized.Now(),
			); ok && retryAfter > delay {
				// Retry-After is a server admission-control signal, not an
				// exponential-backoff input. Preserve it exactly so the
				// caller's deadline can reject a wait it cannot afford.
				delay = retryAfter
			}
		}
		now := normalized.Now()
		if deadline, exists := ctx.Deadline(); exists &&
			!now.Add(delay).Before(deadline) {
			return response, attempts, context.DeadlineExceeded
		}

		if response != nil {
			drainAndClose(response.Body)
		}
		if err := normalized.Sleep(ctx, delay); err != nil {
			return nil, attempts, err
		}
	}
	return nil, attempts, ErrNoResult
}

func cloneAttemptRequest(request *http.Request, ctx context.Context, number int) (*http.Request, error) {
	cloned := request.Clone(ctx)
	if request.Body == nil || request.Body == http.NoBody {
		return cloned, nil
	}
	if request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, errors.Join(ErrBodyNotReplayable, err)
		}
		cloned.Body = body
		return cloned, nil
	}
	if number == 1 {
		cloned.Body = request.Body
		return cloned, nil
	}
	return nil, ErrBodyNotReplayable
}

func shouldRetry(response *http.Response, err error, policy normalizedRetryPolicy) bool {
	if err != nil {
		return policy.RetryError(err)
	}
	if response == nil {
		return false
	}
	_, retry := policy.statusCodes[response.StatusCode]
	return retry
}

func retryDelay(policy normalizedRetryPolicy, retryNumber int) time.Duration {
	delay := policy.InitialBackoff
	for step := 0; step < retryNumber; step++ {
		if delay >= policy.MaxBackoff/2 {
			delay = policy.MaxBackoff
			break
		}
		delay *= 2
	}
	if delay > policy.MaxBackoff {
		delay = policy.MaxBackoff
	}
	if policy.JitterFraction == 0 || delay == 0 {
		return delay
	}
	random := policy.Random()
	if math.IsNaN(random) || math.IsInf(random, 0) {
		random = 0.5
	}
	random = min(1, max(0, random))
	factor := 1 - policy.JitterFraction + (2 * policy.JitterFraction * random)
	jittered := time.Duration(float64(delay) * factor)
	if jittered < 0 {
		return 0
	}
	return min(jittered, policy.MaxBackoff)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64(maxRetryAfter/time.Second) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	deadline, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := deadline.Sub(now)
	if delay < 0 {
		return 0, true
	}
	if delay > maxRetryAfter {
		return 0, false
	}
	return delay, true
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrainBytes))
	_ = body.Close()
}

func validIdempotencyKey(value string) bool {
	if value == "" || len(value) > maxIdempotencyBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func attemptErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "transport_error"
	}
}

// RetryConfig is the deprecated generic callback retry contract.
type RetryConfig struct {
	MaxRetries  int
	Backoff     algorithm.Backoff
	ShouldRetry func(err error) bool
}

type RetryResult[T any] struct {
	Value    T
	Err      error
	Attempts int
}

// Retry executes a generic callback. Zero retries means one callback attempt.
//
// Deprecated: use Do for HTTP requests.
func Retry[T any](
	ctx context.Context,
	config RetryConfig,
	callback func(context.Context) (T, error),
) RetryResult[T] {
	var result RetryResult[T]
	if ctx == nil || callback == nil || config.MaxRetries < 0 {
		result.Err = ErrInvalidPolicy
		return result
	}
	if config.Backoff == nil {
		config.Backoff = algorithm.DefaultExponentialBackoff()
	}
	if config.ShouldRetry == nil {
		config.ShouldRetry = func(err error) bool { return err != nil }
	}
	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			result.Err = err
			return result
		}
		result.Attempts = attempt + 1
		value, err := callback(ctx)
		result.Value, result.Err = value, err
		if err == nil || !config.ShouldRetry(err) || attempt == config.MaxRetries {
			return result
		}
		if err := sleepContext(ctx, config.Backoff.Delay(attempt)); err != nil {
			result.Err = err
			return result
		}
	}
	return result
}

func RetryVoid(ctx context.Context, config RetryConfig, callback func(context.Context) error) error {
	result := Retry(ctx, config, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, callback(ctx)
	})
	return result.Err
}
