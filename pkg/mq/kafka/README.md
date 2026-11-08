# Kafka

Kafka support is built on IBM Sarama. Producers publish records; consumer groups
process them at least once.

## Shared runtime

Create a `consumer.New` or `consumer.NewBatch` runtime, then inject it:

```go
processor, err := consumer.New(handle,
	consumer.WithWorkers(8),
	consumer.WithQueueCapacity(256),
)
if err != nil {
	return err
}
// Stop the group before closing processor with a shutdown context.
err = group.StartWithRuntime(ctx, []string{"orders"}, processor, onError, deadLetter)
```

The [consumer runtime](../consumer/README.md) owns backpressure, priority,
batching and shutdown. Kafka supplies one ordering stream per topic/partition.
Only the completed contiguous prefix of each source window is marked; DLQ
publication must succeed before marking its message. Key ordering or unordered
mode permits parallel processing within a partition when the service allows it.

Groups start once. `Close` cancels consumption and waits for its loop; an
injected runtime stays caller-owned. `Start` and `StartWithResult` create their
own runtime, with workers bounded by `GOMAXPROCS`, and close it on exit.

## Consumer outcomes

`pkg/mq/consumer` defines the handler contract shared with Forge:

- `Success`: acknowledge the message.
- `Retry`: leave it unmarked and end the current claim so Kafka can deliver it
  again.
- `DeadLetter`: publish it through the configured dead-letter handler, then
  acknowledge it only after that handler succeeds.

```go
import (
	"context"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
	"github.com/huynhanx03/go-common/pkg/mq/kafka"
)

group, err := kafka.NewConsumer(config, "orders-worker")
if err != nil {
	return err
}
defer group.Close()

err = group.StartWithResult(
	ctx,
	[]string{"orders"},
	func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
		if err := processOrder(ctx, message.Value); err != nil {
			return consumer.Retry, err
		}
		return consumer.Success, nil
	},
	errorHandler,
	func(ctx context.Context, message consumer.Message, cause error) error {
		return publishDeadLetter(ctx, message, cause)
	},
	consumer.Recovery,
)
```

The dead-letter handler must return nil only after the message is durably
published. If a handler returns `DeadLetter` without a dead-letter handler, or
publishing fails, the source message remains unacknowledged and is retried.
Handlers should not retain message byte slices after returning. Processing is
at least once; inbox deduplication is an application-level concern.

`Start` remains available for error-only handlers. A nil error maps to
`Success`; an error maps to `Retry`. Use `StartWithResult` when a handler needs
to choose `DeadLetter` explicitly. Shared middleware can be passed to
`StartWithResult`; the runtime already contains handler panics and restores
request IDs.

## Producer

`PublishJSON` serializes a value and publishes it with a caller-selected key.
The producer helpers also propagate the request ID from context into Kafka
headers.
