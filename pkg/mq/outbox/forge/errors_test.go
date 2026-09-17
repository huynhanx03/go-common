package forge

import (
	"context"
	"errors"
	"testing"

	forgecore "github.com/huynhanx03/go-common/pkg/mq/forge"
	"github.com/huynhanx03/go-common/pkg/mq/outbox"
)

func TestClassifyForgeErrors(t *testing.T) {
	t.Parallel()

	unknown := errors.New("unclassified")
	tests := []struct {
		name          string
		input         error
		wantCode      string
		wantRetryable bool
		wantAmbiguous bool
	}{
		{name: "message too large", input: forgecore.ErrMessageTooLarge, wantCode: CodeMessageTooLarge},
		{name: "invalid config", input: forgecore.ErrInvalidConfig, wantCode: CodeInvalidMessage},
		{name: "invalid acknowledgment", input: forgecore.ErrInvalidAck, wantCode: CodeInvalidMessage},
		{name: "empty batch", input: forgecore.ErrEmptyBatch, wantCode: CodeInvalidMessage},
		{name: "batch too large", input: forgecore.ErrBatchTooLarge, wantCode: CodeMessageTooLarge},
		{name: "backpressure", input: forgecore.ErrBackpressure, wantCode: CodeBackpressure, wantRetryable: true},
		{name: "storage full", input: forgecore.ErrStorageFull, wantCode: CodeStorageFull, wantRetryable: true},
		{name: "closed", input: forgecore.ErrClosed, wantCode: CodeClosed, wantRetryable: true},
		{name: "unknown", input: unknown, wantCode: CodePublish, wantRetryable: true, wantAmbiguous: true},
		{
			name: "acknowledgment after admission", input: &forgecore.AcknowledgmentError{
				Level: forgecore.AckFsync, Admitted: true, Cause: forgecore.ErrStorageFull,
			},
			wantCode: CodeStorageFull, wantRetryable: true, wantAmbiguous: true,
		},
		{
			name: "acknowledgment before admission", input: &forgecore.AcknowledgmentError{
				Level: forgecore.AckFsync, Cause: forgecore.ErrStorageFull,
			},
			wantCode: CodeStorageFull, wantRetryable: true,
		},
		{
			name: "acknowledgment after append", input: &forgecore.AcknowledgmentError{
				Level: forgecore.AckFsync, Admitted: true, Appended: true, Cause: forgecore.ErrStorageFull,
			},
			wantCode: CodeStorageFull, wantRetryable: true, wantAmbiguous: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := classify(test.input)
			var failure *outbox.PublishError
			if !errors.As(err, &failure) {
				t.Fatalf("classify(%v) = %T, want *outbox.PublishError", test.input, err)
			}
			if failure.Code != test.wantCode || failure.Retryable != test.wantRetryable || failure.Ambiguous != test.wantAmbiguous {
				t.Fatalf("classify(%v) = %+v", test.input, failure)
			}
			if !errors.Is(failure, test.input) {
				t.Fatalf("classified error does not preserve cause %v", test.input)
			}
		})
	}
}

func TestContextCancellationIsPreserved(t *testing.T) {
	t.Parallel()

	for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := classify(cancellation); got != cancellation {
			t.Fatalf("classify(%v) = %v, want original context error", cancellation, got)
		}
		wrapped := &forgecore.AcknowledgmentError{Level: forgecore.AckFsync, Admitted: true, Cause: cancellation}
		if got := classify(wrapped); got != cancellation {
			t.Fatalf("classify(wrapped %v) = %v, want original context error", cancellation, got)
		}
	}
}

func TestNilContextIsPermanentInvalidMessage(t *testing.T) {
	t.Parallel()

	publisher, err := newPublisher(map[string]routeSender{"events": &recordingSender{}})
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	_, err = publisher.Publish(nil, validMessage())
	var failure *outbox.PublishError
	if !errors.As(err, &failure) || failure.Code != CodeInvalidMessage || failure.Retryable || failure.Ambiguous {
		t.Fatalf("Publish(nil) error = %#v", err)
	}
}
