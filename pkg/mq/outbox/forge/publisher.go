package forge

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huynhanx03/go-common/pkg/correlation"
	forgecore "github.com/huynhanx03/go-common/pkg/mq/forge"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

const (
	// HeaderMessageID carries the stable outbox message identity.
	HeaderMessageID = "go-common.outbox.message-id"
	// HeaderCorrelationID carries the end-to-end correlation identity.
	HeaderCorrelationID = "go-common.correlation-id"
	// HeaderAttributePrefix namespaces caller metadata headers.
	HeaderAttributePrefix = "go-common.outbox.attribute."

	CodeRouteNotConfigured = "route_not_configured"
	CodeInvalidMessage     = "invalid_message"
	CodeMessageTooLarge    = "message_too_large"
	CodeBackpressure       = "backpressure"
	CodeStorageFull        = "storage_full"
	CodeClosed             = "closed"
	CodePublish            = "publish_error"
)

const (
	maximumRoutes         = 1024
	maximumRouteBytes     = 256
	maximumMessageIDBytes = 256
	maximumAttributes     = 16
	maximumAttributeBytes = 64
	maximumValueBytes     = 1024
)

// ErrInvalidRoutes reports an empty, unsafe, or nil producer allowlist.
var ErrInvalidRoutes = errors.New("outbox forge: invalid routes")

type routeSender interface {
	SendContext(context.Context, []byte, []byte, []forgecore.Header, forgecore.Acknowledgment) error
}

// Publisher sends an outbox message through a destination allowlist. It does
// not own or close producers and is safe for concurrent use when the supplied
// producers are safe for concurrent use.
type Publisher struct {
	routes map[string]routeSender
}

// NewPublisher constructs an immutable destination allowlist. The caller
// retains ownership of every producer and must close them separately.
func NewPublisher(routes map[string]*forgecore.Producer) (*Publisher, error) {
	if len(routes) == 0 || len(routes) > maximumRoutes {
		return nil, ErrInvalidRoutes
	}
	converted := make(map[string]routeSender, len(routes))
	for destination, producer := range routes {
		converted[destination] = producer
	}
	return newPublisher(converted)
}

func newPublisher(routes map[string]routeSender) (*Publisher, error) {
	if len(routes) == 0 || len(routes) > maximumRoutes {
		return nil, ErrInvalidRoutes
	}
	cloned := make(map[string]routeSender, len(routes))
	for destination, sender := range routes {
		if !validText(destination, maximumRouteBytes, false) || isNil(sender) {
			return nil, ErrInvalidRoutes
		}
		cloned[strings.Clone(destination)] = sender
	}
	return &Publisher{routes: cloned}, nil
}

// Publish sends message with AckFsync durability. A successful local append
// has no transport reference, so the returned PublishAck.Reference is empty.
func (publisher *Publisher) Publish(ctx context.Context, message outbox.Message) (outbox.PublishAck, error) {
	if ctx == nil {
		return outbox.PublishAck{}, permanent(CodeInvalidMessage, nil)
	}
	if err := ctx.Err(); err != nil {
		return outbox.PublishAck{}, err
	}
	if publisher == nil {
		return outbox.PublishAck{}, permanent(CodeRouteNotConfigured, nil)
	}
	sender, exists := publisher.routes[message.Destination]
	if !exists {
		return outbox.PublishAck{}, permanent(CodeRouteNotConfigured, nil)
	}
	headers, err := infrastructureHeaders(message)
	if err != nil {
		return outbox.PublishAck{}, err
	}
	err = sender.SendContext(
		ctx,
		message.Key,
		message.Payload,
		headers,
		forgecore.AckFsync,
	)
	if err != nil {
		return outbox.PublishAck{}, classify(err)
	}
	return outbox.PublishAck{}, nil
}

func infrastructureHeaders(message outbox.Message) ([]forgecore.Header, error) {
	if !validText(message.ID, maximumMessageIDBytes, false) ||
		!validText(message.Destination, maximumRouteBytes, false) ||
		len(message.Metadata.Attributes) > maximumAttributes {
		return nil, permanent(CodeInvalidMessage, nil)
	}
	cid := message.Metadata.CorrelationID
	if cid == "" {
		cid = correlation.New()
	} else if correlation.Validate(cid) != nil {
		return nil, permanent(CodeInvalidMessage, nil)
	}
	headers := make([]forgecore.Header, 0, len(message.Metadata.Attributes)+2)
	headers = append(headers,
		forgecore.Header{Key: []byte(HeaderMessageID), Value: []byte(message.ID)},
		forgecore.Header{Key: []byte(HeaderCorrelationID), Value: []byte(cid)},
	)
	seen := make(map[string]struct{}, len(message.Metadata.Attributes))
	for _, attribute := range message.Metadata.Attributes {
		if !validAttributeKey(attribute.Key) || !validText(attribute.Value, maximumValueBytes, true) {
			return nil, permanent(CodeInvalidMessage, nil)
		}
		normalized := strings.ToLower(attribute.Key)
		if _, duplicate := seen[normalized]; duplicate {
			return nil, permanent(CodeInvalidMessage, nil)
		}
		seen[normalized] = struct{}{}
		headers = append(headers, forgecore.Header{
			Key: []byte(HeaderAttributePrefix + attribute.Key), Value: []byte(attribute.Value),
		})
	}
	return headers, nil
}

func classify(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var acknowledgment *forgecore.AcknowledgmentError
	if errors.As(err, &acknowledgment) && acknowledgment != nil {
		code, retryable := classification(acknowledgment.Cause)
		return &outbox.PublishError{
			// An admitted record may still be flushed by the producer after this
			// call returns. Both admission and append therefore make a retry
			// potentially duplicate the stable message identity.
			Code: code, Retryable: retryable,
			Ambiguous: acknowledgment.Admitted || acknowledgment.Appended,
			Cause:     err,
		}
	}
	code, retryable := classification(err)
	ambiguous := code == CodePublish
	return &outbox.PublishError{Code: code, Retryable: retryable, Ambiguous: ambiguous, Cause: err}
}

func classification(err error) (string, bool) {
	switch {
	case errors.Is(err, forgecore.ErrMessageTooLarge),
		errors.Is(err, forgecore.ErrBatchTooLarge):
		return CodeMessageTooLarge, false
	case errors.Is(err, forgecore.ErrInvalidConfig),
		errors.Is(err, forgecore.ErrInvalidAck),
		errors.Is(err, forgecore.ErrEmptyBatch):
		return CodeInvalidMessage, false
	case errors.Is(err, forgecore.ErrBackpressure):
		return CodeBackpressure, true
	case errors.Is(err, forgecore.ErrStorageFull):
		return CodeStorageFull, true
	case errors.Is(err, forgecore.ErrClosed):
		return CodeClosed, true
	default:
		return CodePublish, true
	}
}

func permanent(code string, cause error) error {
	return &outbox.PublishError{Code: code, Cause: cause}
}

func validAttributeKey(value string) bool {
	if len(value) == 0 || len(value) > maximumAttributeBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validText(value string, maximum int, allowEmpty bool) bool {
	if (!allowEmpty && len(value) == 0) || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
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
