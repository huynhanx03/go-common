# Outbox to Temporal

`pkg/mq/outbox/temporal` implements the existing `outbox.Publisher` contract.
The relay claims a committed message, this adapter validates and decodes it,
then starts a Temporal Workflow selected by a configured destination route.
The adapter borrows the SDK client and does not own the relay, Store, worker, or
business transaction.

## Configure fixed routes

Each destination maps to one Workflow type and task queue. The incoming message
cannot select either. The application supplies decoding and a stable Workflow
ID derived from its operation identity or the outbox message ID.

```go
publisher, err := temporaloutbox.NewPublisher(temporalClient.SDK(), map[string]temporaloutbox.Route{
	"booking.wakeup": {
		Workflow:  "bookingWakeup",
		TaskQueue: "booking-operations",
		Decode: func(_ context.Context, message outbox.Message) (any, error) {
			var input booking.WakeupInput
			err := json.Unmarshal(message.Payload, &input)
			return input, err
		},
		WorkflowID: func(message outbox.Message) (string, error) {
			return "booking-wakeup:" + message.ID, nil
		},
	},
})
if err != nil {
	return err
}
```

Use the application's database transaction to commit its state change and
outbox row together. The relay only sees that message after the transaction
commits. The publisher does not make a Temporal start atomic with the database
commit.

## Delivery and idempotency

Outbox delivery is at least once. A relay can lose the database acknowledgment
after Temporal accepted a start, then deliver the same message again. The
publisher forces the route's stable Workflow ID and uses duplicate-rejecting
Temporal policies. A duplicate start is acknowledged as success for this
explicit route contract. A retry must resolve to the same Workflow ID; do not
generate a new ID per attempt.

Temporal retains closed Workflow IDs only for the Namespace's retention period.
If business idempotency must last longer, store an operation record or unique
constraint in the application database. Workflow Activities also need
idempotency because an Activity can repeat after an external effect succeeds
but before Temporal records completion.

Invalid routes, IDs, or payloads are classified as permanent and the relay can
move them to its terminal state. Unavailable service, resource exhaustion, and
uncertain start outcomes are retryable and marked ambiguous. Native Temporal
errors remain available as causes. The adapter does not add a second retry
loop or change the relay's lease and finalization rules.

Only validated outbox `correlation_id` metadata is propagated into the start
context. Payload and credentials are not logged. Applications own message
schemas, destination names, route rollout, retention decisions, and business
recovery.
