package outbox

import (
	"fmt"
	"reflect"
	"time"
)

const (
	maximumRelayConcurrency     = 4096
	maximumRelayMessageBytes    = 64 << 20
	maximumRelayOperationBudget = 24 * time.Hour
	maximumRelayLeaseDuration   = 30 * 24 * time.Hour
	maximumRelayPollInterval    = 24 * time.Hour
	maximumRelayRetryDelay      = 30 * 24 * time.Hour
)

// Options configures one Relay.
type Options struct {
	// Name is the bounded lifecycle and observability component name.
	Name string
	// Owner identifies this relay instance in Store lease claims.
	Owner string
	// Concurrency is the hard maximum number of simultaneous publishes.
	Concurrency int
	// ClaimLimit bounds each Store claim and cannot exceed Concurrency.
	ClaimLimit int
	// Destinations is the immutable allowlist this relay may claim. Empty means
	// all destinations and preserves the generic relay behavior. Configure it
	// whenever a Publisher only owns a subset of an application's outbox.
	Destinations []string
	// MaxMessageBytes bounds Key plus Payload bytes accepted from Store. It is
	// caller-configured because the relay does not prescribe a payload schema.
	MaxMessageBytes int
	// MaxAttempts moves a retryable failure to dead state at this one-based
	// attempt. Zero selects no relay-imposed attempt ceiling.
	MaxAttempts uint32
	// LeaseDuration must exceed all Store, publish, and finalization budgets.
	LeaseDuration time.Duration
	// PublishTimeout bounds one Publisher call.
	PublishTimeout time.Duration
	// StoreTimeout bounds one Store claim.
	StoreTimeout time.Duration
	// FinalizeTimeout bounds one terminal Store transition, including shutdown.
	FinalizeTimeout time.Duration
	// PollInterval is the correctness fallback when Wakeup is absent or lost.
	PollInterval time.Duration
	// Backoff provides a relative retry delay. Panic or negative output falls
	// back to PollInterval; an explicit zero remains valid.
	Backoff Backoff
}

func validateOptions(store Store, publisher Publisher, options Options) error {
	if isNil(store) || isNil(publisher) || isNil(options.Backoff) {
		return ErrInvalidOptions
	}
	if !validBoundedText(options.Name, 64) || !validBoundedText(options.Owner, 256) {
		return ErrInvalidOptions
	}
	if options.Concurrency <= 0 ||
		options.Concurrency > maximumRelayConcurrency ||
		options.ClaimLimit <= 0 ||
		options.ClaimLimit > options.Concurrency {
		return ErrInvalidOptions
	}
	if !validDestinations(options.Destinations) {
		return ErrInvalidOptions
	}
	if options.MaxMessageBytes <= 0 || options.MaxMessageBytes > maximumRelayMessageBytes {
		return ErrInvalidOptions
	}
	if options.PublishTimeout <= 0 ||
		options.PublishTimeout > maximumRelayOperationBudget ||
		options.StoreTimeout <= 0 ||
		options.StoreTimeout > maximumRelayOperationBudget ||
		options.FinalizeTimeout <= 0 ||
		options.FinalizeTimeout > maximumRelayOperationBudget ||
		options.PollInterval <= 0 ||
		options.PollInterval > maximumRelayPollInterval ||
		options.LeaseDuration <= 0 ||
		options.LeaseDuration > maximumRelayLeaseDuration {
		return ErrInvalidOptions
	}
	remaining := options.LeaseDuration - options.StoreTimeout
	if remaining <= 0 || remaining <= options.PublishTimeout {
		return fmt.Errorf("%w: lease duration must exceed operation budgets", ErrInvalidOptions)
	}
	remaining -= options.PublishTimeout
	if remaining <= options.FinalizeTimeout {
		return fmt.Errorf("%w: lease duration must exceed operation budgets", ErrInvalidOptions)
	}
	return nil
}

func validDestinations(destinations []string) bool {
	if len(destinations) > maximumClaimDestinations {
		return false
	}
	seen := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		if !validBoundedText(destination, maximumDestinationBytes) {
			return false
		}
		if _, exists := seen[destination]; exists {
			return false
		}
		seen[destination] = struct{}{}
	}
	return true
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
