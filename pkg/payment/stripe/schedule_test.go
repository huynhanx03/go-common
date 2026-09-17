package stripe

import (
	"context"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const scheduleBody = `{
  "id": "sub_sched_1",
  "object": "subscription_schedule",
  "status": "active",
  "subscription": {"id": "sub_1", "object": "subscription"},
  "phases": [{"start_date": 1700000000}]
}`

func TestCreateAtPeriodEndWritesTwoPhases(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, scheduleBody)

	start := time.Unix(1700000000, 0).UTC()
	end := time.Unix(1702592000, 0).UTC()

	sched, appErr := provider.CreateAtPeriodEnd(context.Background(), &payment.ScheduleAtPeriodEnd{
		SubscriptionRef: "sub_1",
		CurrentPriceRef: "price_1",
		NextPriceRef:    "price_2",
		PeriodStart:     start,
		PeriodEnd:       end,
		IdempotencyKey:  "sched:1",
	})
	if appErr != nil {
		t.Fatalf("CreateAtPeriodEnd returned an error: %v", appErr)
	}

	// captured reflects the last request: the phases update.
	if captured.method != "POST" || captured.path != "/v1/subscription_schedules/sub_sched_1" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if got := captured.formValue("end_behavior"); got != "release" {
		t.Fatalf("end_behavior = %q", got)
	}

	if got := captured.formValue("phases[0][items][0][price]"); got != "price_1" {
		t.Fatalf("phase 0 price = %q", got)
	}

	if got := captured.formValue("phases[1][items][0][price]"); got != "price_2" {
		t.Fatalf("phase 1 price = %q", got)
	}

	// The two writes are scoped so a replay with different params is not rejected.
	if captured.idempotencyKey != "sched:1:phases" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}

	if sched.Ref != "sub_sched_1" || sched.SubscriptionRef != "sub_1" {
		t.Fatalf("schedule = %+v", sched)
	}
}

func TestReleaseSchedule(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, scheduleBody)

	if _, appErr := provider.Release(context.Background(), &payment.ScheduleRef{ScheduleRef: "sub_sched_1", IdempotencyKey: "rel:1"}); appErr != nil {
		t.Fatalf("Release returned an error: %v", appErr)
	}

	if captured.path != "/v1/subscription_schedules/sub_sched_1/release" {
		t.Fatalf("path = %q", captured.path)
	}

	if captured.idempotencyKey != "rel:1" {
		t.Fatalf("idempotency = %q", captured.idempotencyKey)
	}
}

func TestScheduleNilGuards(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, scheduleBody)

	if _, appErr := provider.CreateAtPeriodEnd(context.Background(), nil); appErr == nil {
		t.Error("nil schedule create must be rejected")
	}

	if _, appErr := provider.Release(context.Background(), nil); appErr == nil {
		t.Error("nil release must be rejected")
	}
}
