# Temporal

`pkg/workflow/temporal` wires the Temporal Go SDK into the shared settings, logger,
identity propagation, and process lifecycle packages. It owns the client
connection and worker shutdown behavior. Workflow code, Activities, Schedules,
task queues, retry choices, and business state remain in the importing project;
the native SDK client and worker are available for those APIs.

## Client and worker ownership

Create one client per process and inject it into workers and services. The
worker borrows the client. Stop workers before closing the shared connection.
The worker is not polling when it is constructed; register workflows and
Activities before adding it to the lifecycle runner.

`settings.Temporal` has no production endpoint or namespace defaults. Load
those values from the application's configuration. API keys require TLS, and
the optional CA and client certificate paths configure TLS/mTLS. Native SDK
options remain available through `WithSDKOptions`; tracing can be enabled with
`WithTracing` and the application's OpenTelemetry provider.

An unset dial timeout uses the caller's context without adding another
deadline. A worker's unset shutdown budget is 30 seconds; configured dial and
shutdown budgets are capped at 24 hours.

```go
log := logger.Wrap(zapLogger)
temporalClient, err := temporal.Dial(ctx, log, settings.Temporal{
	Address:    config.Temporal.Address,
	Namespace:  config.Temporal.Namespace,
	TLSEnabled: config.Temporal.TLSEnabled,
	APIKey:     config.Temporal.APIKey,
})
if err != nil {
	return err
}
defer temporalClient.Close()

jobs, err := temporal.NewWorker(temporalClient, settings.TemporalWorker{
	Name:      "booking-worker",
	TaskQueue: "booking-operations",
}, worker.Options{})
if err != nil {
	return err
}
jobs.SDK().RegisterWorkflow(booking.Wakeup)
jobs.SDK().RegisterActivity(booking.Activities)

runner, err := lifecycle.New(log, lifecycle.Options{})
if err != nil {
	return err
}
if err := runner.Add(jobs, lifecycle.ComponentOptions{Required: true}); err != nil {
	return err
}
if err := runner.Run(ctx); err != nil {
	return err
}
```

`Runner.Run` stops workers before returning, so the deferred close releases the
shared client afterward. `Shutdown` is bounded by the configured worker budget.
It cannot forcibly stop an Activity that ignores cancellation.

## Recurring and delayed work

Use the native Schedule API for recurring work. Pick timezone, overlap, and
catch-up behavior for each Schedule. A stable Schedule ID should be created or
updated by one deployment/bootstrap owner, rather than every replica racing to
replace it during startup.

```go
_, err := temporalClient.SDK().ScheduleClient().Create(ctx, client.ScheduleOptions{
	ID: "daily-maintenance",
	Spec: client.ScheduleSpec{
		CronExpressions: []string{"0 2 * * *"},
		TimeZoneName:    "Asia/Ho_Chi_Minh",
	},
	Action:        &client.ScheduleWorkflowAction{Workflow: "dailyWorkflow", TaskQueue: "maintenance"},
	Overlap:       enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	CatchupWindow: 15 * time.Minute,
})
```

For a one-time delayed start, use the SDK's `StartDelay`:

```go
_, err := temporalClient.SDK().ExecuteWorkflow(ctx, client.StartWorkflowOptions{
	ID:         "booking-wakeup:booking-42:revision-3",
	TaskQueue:  "booking-operations",
	StartDelay: 30 * time.Minute,
}, "bookingWakeup", bookingID, revision)
```

If the due time can change or be canceled, use a workflow with a durable timer
and a versioned Signal or Update contract. The workflow stores the current
revision and due time; stale messages are ignored. Its Activity must still
re-read the authoritative database row and conditionally claim the current
revision. This makes the timer safe if it races with a commit or if an old
message is delivered again.

Send a revisioned command through the native SDK client:

```go
type WakeupCommand struct {
	Revision uint64
	DueAt    time.Time
	Cancel   bool
}

err := temporalClient.SDK().SignalWorkflow(ctx, workflowID, "", "booking-wakeup-command", WakeupCommand{
	Revision: nextRevision,
	DueAt:    newDueTime,
})
```

The Workflow accepts only a newer revision, resets or cancels its durable timer,
and asks an Activity to claim the current database revision when the timer
fires. A cancel command uses the same message with `Cancel: true`.

## Workflow and Activity rules

- Workflow code is replayed. Use `workflow.Now`, workflow timers/channels, and
  `workflow.GetLogger`; do not call databases, Redis, HTTP, or the common cache
  directly from a Workflow.
- Put side effects in Activities. They can run more than once if a process
  fails after the external effect but before Temporal records completion. Use
  a database uniqueness constraint, idempotency key, or conditional state
  transition for the business operation.
- Set Activity timeouts and retry classification according to the operation.
  There is no universal retry count or stale-task rule that fits every service.
  Long-running Activities should heartbeat when progress and cancellation need
  to survive worker loss.
- Return a non-retryable ApplicationError for a permanent business rejection
  such as an invalid request; let transient dependency failures follow the
  Workflow's configured retry policy. Choose Activity timeouts and retry
  policies per operation rather than applying one global policy.

  ```go
  if errors.Is(err, ErrInvalidBooking) {
      return temporal.NewNonRetryableApplicationError("booking is invalid", "invalid_booking", nil)
  }
  return temporal.NewApplicationError("booking dependency unavailable", "dependency_unavailable", nil)
  ```

- A Workflow cancellation does not undo a completed external side effect. Make
  Activities idempotent where possible; otherwise model compensation as its
  own explicit Activity and state transition.
- Use `workflow.GetLogger(ctx)` in Workflows. The SDK suppresses ordinary log
  output during replay; direct calls to the common logger inside replayable
  workflow code bypass that behavior.
- Use the native version marker when deploying an incompatible Workflow
  change. Keep the old branch so histories created before deployment replay
  with their original command sequence:

  ```go
  version := workflow.GetVersion(ctx, "booking-wakeup-v2", workflow.DefaultVersion, 1)
  if version == workflow.DefaultVersion {
      return runOriginalWakeup(ctx, input)
  }
  return runUpdatedWakeup(ctx, input)
  ```

  Remove the old branch only after old executions are closed or no longer
  expected to replay. Use Continue-As-New to bound long-running histories.
- Keep Workflow inputs bounded and avoid credentials, authorization claims,
  and full database records in persisted history.

## Durable batch segments

`BatchWorkflow` calls a registered Activity by its fixed project-owned name.
`BatchActivities` runs one bounded `pkg/workflow/batch` segment. The service
provides the transactional Store and page callback; the checkpoint in the
database is authoritative after retries or worker restarts. Heartbeats contain
only the job key and committed version/count, and do not replace checkpoint
reads. Configure the Activity heartbeat timeout above the runner's bounded
open/acquire/read/page budget.

In the example, `temporal` names this go-common package and `temporalSDK`
aliases `go.temporal.io/sdk/temporal` for the SDK retry policy.

```go
activities := temporal.NewBatchActivities(runner, processPage)
jobs.SDK().RegisterWorkflowWithOptions(temporal.BatchWorkflow, workflow.RegisterOptions{Name: "BatchWorkflow"})
jobs.SDK().RegisterActivityWithOptions(activities.Run, activity.RegisterOptions{Name: "runBillingSegment"})

run, err := temporalClient.SDK().ExecuteWorkflow(ctx, client.StartWorkflowOptions{
	ID: "billing/2026-11/partition-03", TaskQueue: "billing-jobs",
}, temporal.BatchWorkflow, temporal.BatchWorkflowInput{
	Definition: batch.Definition{Key: "billing/2026-11/partition-03", InputFingerprint: inputHash, CursorSchema: "invoice-id-v1"},
	ActivityName: "runBillingSegment",
	ActivityOptions: workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:   30 * time.Second,
		RetryPolicy:        &temporalSDK.RetryPolicy{MaximumAttempts: 0},
	},
	ContinueAfterSegments: 100,
})
```

Temporal retries delivery and keeps the orchestration durable; it does not make
an external payment or database operation exactly-once. Write business state
and the checkpoint receipt in one Store transaction, use a stable provider
idempotency key for external effects, and write an outbox row when publication
must follow a database commit. Continue-As-New carries only bounded workflow
configuration and the last result hint; the next Activity still reads durable
state from the Store.

The common propagator carries validated `request_id` and `correlation_id` to
Activities and child Workflows. It does not turn these observability values
into authorization identity, and it does not propagate an HTTP request's
cancellation or deadline into future work.

### Query batch progress

`BatchWorkflow` exposes the most recent successful batch Activity result through
`BatchProgressQueryName`. An empty `runID` queries the current execution:

```go
query, err := temporalClient.SDK().QueryWorkflow(
	ctx,
	workflowID,
	"",
	temporal.BatchProgressQueryName,
)
if err != nil {
	return err
}
var progress batch.Result
if err := query.Get(&progress); err != nil {
	return err
}
```

The query returns a segment-level snapshot, initially `{Key: workflow key}`
with zero progress. It stays at the previous successful segment while an
Activity is running or retrying, and carries across `ContinueAsNew`. Leave
`LastResult` zero when starting a workflow; the Workflow fills it when it
continues itself. The query is for status display; read the Store checkpoint
when the caller needs the exact current durable cursor or page-level progress.

## Local development and tests

The optional Compose profile runs the official file-backed Temporal development
server, with host ports bound to loopback:

```sh
docker compose --profile temporal up -d --wait temporal
```

The named volume preserves local history across container recreation. This
development server is not a production deployment. Real server tests use the
Temporal image pinned in `pkg/testutil/containers/constants.go` and are enabled
with the repository's integration test target.

## Related package

[`pkg/mq/outbox/temporal`](../../mq/outbox/temporal/README.md) starts Workflows from
committed outbox messages using stable Workflow IDs.
