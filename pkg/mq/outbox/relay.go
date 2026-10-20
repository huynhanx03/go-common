package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/correlation"
	"github.com/huynhanx03/go-common/pkg/logger"
)

type relayState uint8

const (
	stateNew relayState = iota
	stateRunning
	stateStopping
	stateStopped
)

const (
	failureInvalidMessage  = "invalid_message"
	failurePublish         = "publish_error"
	failurePublisherPanic  = "publisher_panic"
	failureShutdown        = "shutdown"
	failureMaximumAttempts = "max_attempts_exceeded"
	failureStore           = "store_error"
)

// Relay coordinates claiming, publishing, and finalizing durable messages.
// A Relay has a single-use Run lifecycle and supports concurrent, idempotent
// Shutdown calls.
type Relay struct {
	store                Store
	publisher            Publisher
	wakeup               Wakeup
	options              Options
	destinationAllowlist map[string]struct{}

	mu     sync.Mutex
	state  relayState
	cancel context.CancelFunc
	done   chan struct{}

	invalidBackoffOnce sync.Once
}

// New constructs a relay after validating dependencies and operation budgets.
func New(store Store, publisher Publisher, wakeup Wakeup, options Options) (*Relay, error) {
	if err := validateOptions(store, publisher, options); err != nil {
		return nil, err
	}
	options.Name = strings.Clone(options.Name)
	options.Owner = strings.Clone(options.Owner)
	options.Destinations = cloneStrings(options.Destinations)
	return &Relay{
		store: store, publisher: publisher, wakeup: wakeup, options: options,
		destinationAllowlist: destinationSet(options.Destinations),
		state:                stateNew, done: make(chan struct{}),
	}, nil
}

// Name returns the lifecycle component name.
func (relay *Relay) Name() string {
	if relay == nil {
		return ""
	}
	return relay.options.Name
}

// Run claims and relays messages until ctx is canceled or Shutdown is called.
// Graceful cancellation returns nil after every in-flight claim is finalized.
// A Store contract violation returns ErrStoreContract only after previously
// admitted work is drained.
func (relay *Relay) Run(ctx context.Context) error {
	if relay == nil || relay.done == nil {
		return ErrInvalidState
	}
	ctx = nonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	relay.mu.Lock()
	if relay.state != stateNew {
		relay.mu.Unlock()
		return ErrInvalidState
	}
	runCtx, cancel := context.WithCancel(ctx)
	relay.cancel = cancel
	relay.state = stateRunning
	relay.mu.Unlock()
	runCtx = logger.WithFields(runCtx,
		logger.String("relay", relay.options.Name),
		logger.String("owner", relay.options.Owner),
	)
	logger.FromContext(runCtx).Info("outbox relay started")

	runErr := relay.runLoop(runCtx, cancel)
	cancel()
	logger.FromContext(runCtx).Info("outbox relay stopped")

	relay.mu.Lock()
	relay.state = stateStopped
	close(relay.done)
	relay.mu.Unlock()
	return runErr
}

// Shutdown stops admission and waits for in-flight claims to be finalized. It
// is safe to call concurrently and repeatedly. The supplied context only
// bounds the caller's wait; it does not shorten finalization budgets.
func (relay *Relay) Shutdown(ctx context.Context) error {
	if relay == nil || relay.done == nil {
		return ErrInvalidState
	}
	ctx = nonNilContext(ctx)

	relay.mu.Lock()
	switch relay.state {
	case stateNew:
		relay.state = stateStopped
		close(relay.done)
		relay.mu.Unlock()
		return nil
	case stateRunning:
		relay.state = stateStopping
		relay.cancel()
	case stateStopping:
	case stateStopped:
		relay.mu.Unlock()
		return nil
	default:
		relay.mu.Unlock()
		return ErrInvalidState
	}
	done := relay.done
	relay.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (relay *Relay) runLoop(ctx context.Context, cancel context.CancelFunc) error {
	completed := make(chan struct{}, relay.options.Concurrency)
	inFlight := 0
	pending := make([]ClaimedMessage, 0, relay.options.ClaimLimit)
	wakeupSignals := relay.startWakeupLoop(ctx)
	var fatalErr error

	for {
		if ctx.Err() != nil {
			available := relay.options.Concurrency - inFlight
			if len(pending) > 0 && available > 0 {
				count := min(len(pending), available)
				for _, item := range pending[:count] {
					relay.launch(completed, &inFlight, func() { relay.releaseOnShutdown(ctx, item) })
				}
				pending = pending[count:]
				continue
			}
			if inFlight == 0 && len(pending) == 0 {
				return fatalErr
			}
			<-completed
			inFlight--
			continue
		}

		available := relay.options.Concurrency - inFlight
		if available == 0 {
			select {
			case <-completed:
				inFlight--
			case <-ctx.Done():
			}
			continue
		}
		if len(pending) > 0 {
			count := min(len(pending), available)
			for _, item := range pending[:count] {
				relay.launch(completed, &inFlight, func() { relay.process(ctx, item) })
			}
			pending = pending[count:]
			continue
		}
		limit := min(relay.options.ClaimLimit, available)
		claimed, err := relay.claim(ctx, limit)
		if err != nil {
			if len(claimed) > 0 {
				logger.FromContext(ctx).Error("outbox store returned claims with an error")
				fatalErr = fmt.Errorf(
					"%w: claim returned %d messages with an error",
					ErrStoreContract,
					len(claimed),
				)
				cancel()
				continue
			}
			if ctx.Err() != nil {
				continue
			}
			relay.logStoreFailure(ctx, "claim", err)
			relay.waitForWork(ctx, completed, wakeupSignals, &inFlight)
			continue
		}
		if len(claimed) > limit {
			logger.FromContext(ctx).Error("outbox store exceeded claim limit")
			fatalErr = fmt.Errorf(
				"%w: claim returned %d messages for limit %d",
				ErrStoreContract,
				len(claimed),
				limit,
			)
			cancel()
			continue
		}
		if len(claimed) == 0 {
			relay.waitForWork(ctx, completed, wakeupSignals, &inFlight)
			continue
		}

		seenMessageIDs := make(map[string]struct{}, len(claimed))
		for _, item := range claimed {
			if err := validateLease(item.Lease); err != nil {
				logger.FromContext(ctx).Error("outbox store returned an invalid lease")
				fatalErr = fmt.Errorf("%w: invalid lease", ErrStoreContract)
				cancel()
				break
			}
			if item.Lease.MessageID != item.Message.ID {
				logger.FromContext(ctx).Error("outbox store returned mismatched message identity")
				fatalErr = fmt.Errorf("%w: lease and message identity differ", ErrStoreContract)
				cancel()
				break
			}
			messageID := strings.Clone(item.Lease.MessageID)
			if _, duplicate := seenMessageIDs[messageID]; duplicate {
				logger.FromContext(ctx).Error("outbox store returned duplicate message identity")
				fatalErr = fmt.Errorf("%w: duplicate message identity", ErrStoreContract)
				cancel()
				break
			}
			seenMessageIDs[messageID] = struct{}{}
			if len(relay.destinationAllowlist) > 0 {
				if _, allowed := relay.destinationAllowlist[item.Message.Destination]; !allowed {
					logger.FromContext(ctx).Error("outbox store returned a destination outside its claim allowlist")
					fatalErr = fmt.Errorf("%w: destination outside claim allowlist", ErrStoreContract)
					cancel()
					break
				}
			}
		}
		if fatalErr != nil {
			continue
		}
		for _, item := range claimed {
			if err := validateClaimedMessage(item.Lease, item.Message, relay.options.MaxMessageBytes); err != nil {
				// Retain only the bounded fencing lease. Processing the empty
				// message moves malformed data to the terminal state without
				// cloning untrusted slices or retaining their backing storage.
				pending = append(pending, ClaimedMessage{
					Lease:   cloneLease(item.Lease),
					Message: Message{ID: strings.Clone(item.Lease.MessageID)},
				})
				continue
			}
			pending = append(pending, cloneClaimedMessage(item))
		}
	}
}

func (relay *Relay) launch(completed chan<- struct{}, inFlight *int, operation func()) {
	*inFlight++
	go func() {
		defer func() { completed <- struct{}{} }()
		operation()
	}()
}

func (relay *Relay) releaseOnShutdown(ctx context.Context, claimed ClaimedMessage) {
	if _, err := normalizedMessage(claimed.Lease, claimed.Message, relay.options.MaxMessageBytes); err != nil {
		if markErr := relay.markDead(ctx, claimed.Lease, DeadDisposition{Code: failureInvalidMessage}); markErr != nil {
			relay.logStoreFailure(ctx, "mark_dead", markErr)
		} else {
			relay.logInvalidMessage(ctx, claimed.Lease)
		}
		return
	}
	if err := relay.markRetry(ctx, claimed.Lease, RetryDisposition{Code: failureShutdown}); err != nil {
		relay.logStoreFailure(ctx, "mark_retry", err)
	}
}

func (relay *Relay) claim(ctx context.Context, limit int) (claimed []ClaimedMessage, err error) {
	claimCtx, cancel := context.WithTimeout(ctx, relay.options.StoreTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s: %T", failureStore, recovered)
		}
	}()
	return relay.store.Claim(claimCtx, ClaimRequest{
		Owner: relay.options.Owner, Limit: limit, LeaseDuration: relay.options.LeaseDuration,
		Destinations: cloneStrings(relay.options.Destinations),
	})
}

func (relay *Relay) waitForWork(
	ctx context.Context,
	completed <-chan struct{},
	wakeup <-chan struct{},
	inFlight *int,
) {
	timer := time.NewTimer(relay.options.PollInterval)
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-wakeup:
	case <-completed:
		*inFlight--
	}
}

func (relay *Relay) startWakeupLoop(ctx context.Context) <-chan struct{} {
	if isNil(relay.wakeup) {
		return nil
	}
	signals := make(chan struct{})
	go func() {
		for ctx.Err() == nil {
			err := relay.waitWakeup(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				logger.FromContext(ctx).Debug("outbox wakeup unavailable; polling remains active")
				timer := time.NewTimer(relay.options.PollInterval)
				select {
				case <-ctx.Done():
					stopTimer(timer)
					return
				case <-timer.C:
				}
				continue
			}
			select {
			case signals <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return signals
}

func (relay *Relay) waitWakeup(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("wakeup panic: %T", recovered)
		}
	}()
	return relay.wakeup.Wait(ctx)
}

func (relay *Relay) process(ctx context.Context, claimed ClaimedMessage) {
	message, err := normalizedMessage(claimed.Lease, claimed.Message, relay.options.MaxMessageBytes)
	if err != nil {
		if markErr := relay.markDead(ctx, claimed.Lease, DeadDisposition{Code: failureInvalidMessage}); markErr != nil {
			relay.logStoreFailure(ctx, "mark_dead", markErr)
		} else {
			relay.logInvalidMessage(ctx, claimed.Lease)
		}
		return
	}

	messageCtx := correlation.WithContext(ctx, message.Metadata.CorrelationID)
	fields := []logger.Field{
		logger.String("message_id", message.ID),
		logger.String("destination", message.Destination),
		logger.String("attempt", strconv.FormatUint(uint64(claimed.Lease.Attempt), 10)),
	}
	messageCtx = logger.WithFields(messageCtx, fields...)
	logger.FromContext(messageCtx).Debug("outbox message claimed")

	ack, publishErr := relay.publish(messageCtx, message)
	if publishErr == nil {
		ack = normalizedPublishAck(ack)
		if err := relay.markPublished(messageCtx, claimed.Lease, ack); err != nil {
			relay.logStoreFailure(messageCtx, "mark_published", err)
			return
		}
		logger.FromContext(messageCtx).Debug("outbox message published")
		return
	}

	disposition, retryable := relay.classifyFailure(messageCtx, claimed.Lease.Attempt, publishErr)
	if retryable {
		if err := relay.markRetry(messageCtx, claimed.Lease, disposition.retry); err != nil {
			relay.logStoreFailure(messageCtx, "mark_retry", err)
			return
		}
		logger.FromContext(logger.WithFields(messageCtx,
			logger.String("failure_code", disposition.retry.Code),
			logger.String("retry_delay", disposition.retry.Delay.String()),
			logger.String("ambiguous", strconv.FormatBool(disposition.retry.Ambiguous)),
		)).Warn("outbox message scheduled for retry")
		return
	}
	if err := relay.markDead(messageCtx, claimed.Lease, disposition.dead); err != nil {
		relay.logStoreFailure(messageCtx, "mark_dead", err)
		return
	}
	logger.FromContext(logger.WithFields(messageCtx,
		logger.String("failure_code", disposition.dead.Code),
		logger.String("ambiguous", strconv.FormatBool(disposition.dead.Ambiguous)),
	)).Error("outbox message marked dead")
}

type failureDisposition struct {
	retry RetryDisposition
	dead  DeadDisposition
}

func (relay *Relay) classifyFailure(ctx context.Context, attempt uint32, failure error) (failureDisposition, bool) {
	if ctx.Err() != nil {
		return failureDisposition{retry: RetryDisposition{
			Code: failureShutdown, Ambiguous: true,
		}}, true
	}

	code, retryable, ambiguous := failurePublish, true, true
	var classified *PublishError
	if errors.As(failure, &classified) && classified != nil {
		if validBoundedText(classified.Code, maximumAttributeKeyBytes) {
			code = strings.Clone(classified.Code)
		}
		retryable = classified.Retryable
		ambiguous = classified.Ambiguous
	}
	if !retryable {
		return failureDisposition{dead: DeadDisposition{Code: code, Ambiguous: ambiguous}}, false
	}
	if relay.options.MaxAttempts > 0 && attempt >= relay.options.MaxAttempts {
		return failureDisposition{dead: DeadDisposition{
			Code: failureMaximumAttempts, Ambiguous: ambiguous,
		}}, false
	}
	delay := relay.backoff(ctx, attempt)
	return failureDisposition{retry: RetryDisposition{
		Delay: delay, Code: code, Ambiguous: ambiguous,
	}}, true
}

func (relay *Relay) publish(ctx context.Context, message Message) (ack PublishAck, err error) {
	publishCtx, cancel := context.WithTimeout(ctx, relay.options.PublishTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PublishError{Code: failurePublisherPanic, Retryable: true, Ambiguous: true}
		}
	}()
	return relay.publisher.Publish(publishCtx, message)
}

func (relay *Relay) backoff(ctx context.Context, attempt uint32) (delay time.Duration) {
	valid := true
	defer func() {
		if recover() != nil {
			valid = false
		}
		if delay < 0 || delay > maximumRelayRetryDelay {
			valid = false
		}
		if !valid {
			delay = relay.options.PollInterval
			relay.invalidBackoffOnce.Do(func() {
				logger.FromContext(logger.WithFields(ctx,
					logger.String("failure_code", "invalid_backoff"),
				)).Warn("outbox backoff returned an invalid delay")
			})
		}
	}()
	return relay.options.Backoff.Delay(attempt)
}

func (relay *Relay) markPublished(ctx context.Context, lease Lease, ack PublishAck) error {
	return relay.finalize(ctx, func(finalizeCtx context.Context) error {
		return relay.store.MarkPublished(finalizeCtx, lease, ack)
	})
}

func (relay *Relay) markRetry(ctx context.Context, lease Lease, disposition RetryDisposition) error {
	return relay.finalize(ctx, func(finalizeCtx context.Context) error {
		return relay.store.MarkRetry(finalizeCtx, lease, disposition)
	})
}

func (relay *Relay) markDead(ctx context.Context, lease Lease, disposition DeadDisposition) error {
	return relay.finalize(ctx, func(finalizeCtx context.Context) error {
		return relay.store.MarkDead(finalizeCtx, lease, disposition)
	})
}

func (relay *Relay) finalize(ctx context.Context, operation func(context.Context) error) (err error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(nonNilContext(ctx)), relay.options.FinalizeTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s: %T", failureStore, recovered)
		}
	}()
	return operation(cleanupCtx)
}

func (relay *Relay) logStoreFailure(ctx context.Context, operation string, failure error) {
	code := failureStore
	switch {
	case errors.Is(failure, ErrLeaseLost):
		code = "lease_lost"
	case errors.Is(failure, context.DeadlineExceeded):
		code = "store_timeout"
	case errors.Is(failure, context.Canceled):
		code = "store_canceled"
	}
	logger.FromContext(logger.WithFields(ctx,
		logger.String("operation", operation),
		logger.String("failure_code", code),
		logger.String("error_type", fmt.Sprintf("%T", failure)),
	)).Error("outbox store operation failed")
}

func (relay *Relay) logInvalidMessage(ctx context.Context, lease Lease) {
	logger.FromContext(logger.WithFields(ctx,
		logger.String("message_id", lease.MessageID),
		logger.String("attempt", strconv.FormatUint(uint64(lease.Attempt), 10)),
		logger.String("failure_code", failureInvalidMessage),
	)).Error("outbox message marked dead")
}

func normalizedMessage(lease Lease, message Message, maximumDataBytes int) (Message, error) {
	if err := validateClaimedMessage(lease, message, maximumDataBytes); err != nil {
		return Message{}, ErrInvalidMessage
	}
	if message.Metadata.CorrelationID == "" {
		message.Metadata.CorrelationID = fallbackCorrelationID(message.ID)
	}
	return message, nil
}

func cloneClaimedMessage(claimed ClaimedMessage) ClaimedMessage {
	claimed.Lease = cloneLease(claimed.Lease)
	claimed.Message = cloneMessage(claimed.Message)
	return claimed
}

func cloneLease(lease Lease) Lease {
	lease.MessageID = strings.Clone(lease.MessageID)
	lease.Token = strings.Clone(lease.Token)
	return lease
}

func cloneMessage(message Message) Message {
	message.ID = strings.Clone(message.ID)
	message.Destination = strings.Clone(message.Destination)
	message.Key = append([]byte(nil), message.Key...)
	message.Payload = append([]byte(nil), message.Payload...)
	message.Metadata.CorrelationID = strings.Clone(message.Metadata.CorrelationID)
	attributes := make([]Attribute, len(message.Metadata.Attributes))
	for index, attribute := range message.Metadata.Attributes {
		attributes[index] = Attribute{
			Key: strings.Clone(attribute.Key), Value: strings.Clone(attribute.Value),
		}
	}
	message.Metadata.Attributes = attributes
	return message
}

func cloneStrings(values []string) []string {
	cloned := make([]string, len(values))
	for index, value := range values {
		cloned[index] = strings.Clone(value)
	}
	return cloned
}

func destinationSet(destinations []string) map[string]struct{} {
	if len(destinations) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		set[destination] = struct{}{}
	}
	return set
}

func fallbackCorrelationID(messageID string) string {
	digest := sha256.Sum256([]byte(messageID))
	return "relay-" + hex.EncodeToString(digest[:16])
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
