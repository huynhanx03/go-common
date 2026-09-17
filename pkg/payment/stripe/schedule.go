package stripe

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// scheduleEndBehaviorRelease releases the subscription back to normal billing
// once the scheduled phases finish.
const scheduleEndBehaviorRelease = "release"

// CreateAtPeriodEnd builds a two-phase schedule: the current price until the
// period ends, then the next price. It is done as create-then-update because
// Stripe seeds the schedule from the live subscription first.
func (p *Provider) CreateAtPeriodEnd(ctx context.Context, in *payment.ScheduleAtPeriodEnd) (*payment.Schedule, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil schedule request")
	}

	createParams := &stripesdk.SubscriptionScheduleCreateParams{
		FromSubscription: stripesdk.String(in.SubscriptionRef),
	}
	setIdempotency(&createParams.Params, scopeKey(in.IdempotencyKey, "create"))

	created, err := p.client.V1SubscriptionSchedules.Create(ctx, createParams)
	if err != nil {
		return nil, p.fail("stripe subscription schedule create failed", err)
	}

	phaseStart := in.PeriodStart
	if start, ok := firstPhaseStart(created); ok {
		phaseStart = start
	}

	if phaseStart.IsZero() {
		return nil, payment.ProviderContractBroken("stripe subscription has no current period start for schedule phase")
	}

	return p.writeSchedulePhases(ctx, created.ID, phaseStart, in)
}

// Release ends a schedule and returns the subscription to normal billing.
func (p *Provider) Release(ctx context.Context, in *payment.ScheduleRef) (*payment.Schedule, *apperr.AppError) {
	if in == nil {
		return nil, payment.Invalid("stripe: nil schedule request")
	}

	params := &stripesdk.SubscriptionScheduleReleaseParams{}
	setIdempotency(&params.Params, in.IdempotencyKey)

	released, err := p.client.V1SubscriptionSchedules.Release(ctx, in.ScheduleRef, params)
	if err != nil {
		return nil, p.fail("stripe subscription schedule release failed", err)
	}

	return scheduleToNeutral(released), nil
}

func (p *Provider) writeSchedulePhases(ctx context.Context, scheduleRef string, phaseStart time.Time, in *payment.ScheduleAtPeriodEnd) (*payment.Schedule, *apperr.AppError) {
	current := schedulePhaseParams(in.CurrentPriceRef)
	current.StartDate = stripesdk.Int64(phaseStart.Unix())
	current.EndDate = stripesdk.Int64(in.PeriodEnd.Unix())

	next := schedulePhaseParams(in.NextPriceRef)
	next.Metadata = in.NextPhaseMetadata

	params := &stripesdk.SubscriptionScheduleUpdateParams{
		EndBehavior:       stripesdk.String(scheduleEndBehaviorRelease),
		ProrationBehavior: stripesdk.String(payment.ProrationNone.String()),
		Phases: []*stripesdk.SubscriptionScheduleUpdatePhaseParams{
			current,
			next,
		},
	}
	setIdempotency(&params.Params, scopeKey(in.IdempotencyKey, "phases"))

	updated, err := p.client.V1SubscriptionSchedules.Update(ctx, scheduleRef, params)
	if err != nil {
		return nil, p.fail("stripe subscription schedule update failed", err)
	}

	return scheduleToNeutral(updated), nil
}

func schedulePhaseParams(priceRef string) *stripesdk.SubscriptionScheduleUpdatePhaseParams {
	return &stripesdk.SubscriptionScheduleUpdatePhaseParams{
		Items: []*stripesdk.SubscriptionScheduleUpdatePhaseItemParams{
			{
				Price:    stripesdk.String(priceRef),
				Quantity: stripesdk.Int64(1),
			},
		},
		ProrationBehavior: stripesdk.String(payment.ProrationNone.String()),
	}
}

func firstPhaseStart(src *stripesdk.SubscriptionSchedule) (time.Time, bool) {
	if src == nil || len(src.Phases) == 0 || src.Phases[0] == nil || src.Phases[0].StartDate == 0 {
		return time.Time{}, false
	}

	return time.Unix(src.Phases[0].StartDate, 0).UTC(), true
}

func scheduleToNeutral(src *stripesdk.SubscriptionSchedule) *payment.Schedule {
	if src == nil {
		return nil
	}

	out := &payment.Schedule{
		Ref:    src.ID,
		Status: payment.ScheduleStatus(src.Status),
	}

	if src.Subscription != nil {
		out.SubscriptionRef = src.Subscription.ID
	}

	return out
}
