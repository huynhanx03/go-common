package payment

import "time"

// ScheduleStatus is the neutral status of a subscription schedule.
type ScheduleStatus string

const (
	ScheduleStatusNotStarted ScheduleStatus = "not_started"
	ScheduleStatusActive     ScheduleStatus = "active"
	ScheduleStatusCompleted  ScheduleStatus = "completed"
	ScheduleStatusReleased   ScheduleStatus = "released"
	ScheduleStatusCanceled   ScheduleStatus = "canceled"
)

// Schedule is the neutral projection of a subscription schedule: a planned
// sequence of phases the subscription moves through.
type Schedule struct {
	Ref             string
	SubscriptionRef string
	Status          ScheduleStatus
}

// ScheduleAtPeriodEnd plans a two-phase schedule that keeps the current price
// until the period ends, then switches to the next price.
type ScheduleAtPeriodEnd struct {
	SubscriptionRef string
	CurrentPriceRef string
	NextPriceRef    string
	PeriodStart     time.Time
	PeriodEnd       time.Time

	NextPhaseMetadata map[string]string
	IdempotencyKey    string
}

// ScheduleRef names a schedule for an idempotent mutation.
type ScheduleRef struct {
	ScheduleRef    string
	IdempotencyKey string
}
