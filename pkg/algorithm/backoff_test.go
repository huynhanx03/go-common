package algorithm

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestBackoffRejectsInvalidInputsAndSaturates(t *testing.T) {
	t.Parallel()

	if _, err := CalculateBackoff(-1, time.Second, time.Minute, nil); !errors.Is(
		err,
		ErrInvalidBackoff,
	) {
		t.Fatalf("negative attempt error = %v", err)
	}
	if _, err := CalculateBackoff(1, 0, time.Minute, nil); !errors.Is(
		err,
		ErrInvalidBackoff,
	) {
		t.Fatalf("zero base error = %v", err)
	}
	delay, err := CalculateBackoff(math.MaxInt, time.Second, time.Minute, nil)
	if err != nil || delay != time.Minute {
		t.Fatalf("saturated delay = %s, %v", delay, err)
	}
}

func TestBackoffPoliciesFollowSequencesAndCap(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy Backoff
		want   []time.Duration
	}{
		{"constant", NewConstantBackoff(4 * time.Second), []time.Duration{4, 4, 4, 4}},
		{"linear", NewLinearBackoff(4*time.Second, 10*time.Second), []time.Duration{4, 8, 10, 10}},
		{"exponential", NewExponentialBackoff(time.Second, 10*time.Second, 3), []time.Duration{1, 3, 9, 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for attempt, seconds := range test.want {
				if got := test.policy.Delay(attempt); got != seconds*time.Second {
					t.Fatalf("attempt %d: delay %v, want %v", attempt, got, seconds*time.Second)
				}
			}
		})
	}
	for name, policy := range map[string]Backoff{
		"linear overflow":      NewLinearBackoff(4*time.Second, 10*time.Second),
		"exponential overflow": NewExponentialBackoff(time.Second, 10*time.Second, 2),
	} {
		t.Run(name, func(t *testing.T) {
			if got := policy.Delay(math.MaxInt); got != 10*time.Second {
				t.Fatalf("large attempt overflowed cap: %v", got)
			}
		})
	}
	t.Run("largest duration", func(t *testing.T) {
		if got := NewExponentialBackoff(time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), 2).Delay(0); got != time.Duration(math.MaxInt64) {
			t.Fatalf("largest representable delay overflowed: %v", got)
		}
	})
}

func TestBackoffDefaultsAndJitterStayWithinPolicy(t *testing.T) {
	for _, policy := range []Backoff{NewConstantBackoff(0), NewLinearBackoff(0, 0), NewExponentialBackoff(0, 0, 1)} {
		if got := policy.Delay(0); got != 100*time.Millisecond {
			t.Fatalf("default first delay = %v", got)
		}
	}
	for _, test := range []struct {
		policy Backoff
		base   time.Duration
	}{
		{NewJitterBackoff(NewConstantBackoff(time.Second)), time.Second},
		{DefaultExponentialBackoff(), 100 * time.Millisecond},
	} {
		for range 64 {
			if got := test.policy.Delay(0); got < test.base/2 || got > test.base {
				t.Fatalf("jitter escaped [%v,%v]: %v", test.base/2, test.base, got)
			}
		}
	}
}

func TestCheckedBackoffRejectsBadPolicyAndBoundsJitter(t *testing.T) {
	for _, test := range []struct {
		attempt   int
		base, max time.Duration
		jitter    BackoffJitter
	}{
		{-1, 1, 10, nil}, {0, 0, 10, nil}, {0, 2, 1, nil},
		{0, 1, 10, func(time.Duration) time.Duration { return -1 }},
	} {
		if _, err := CalculateBackoff(test.attempt, test.base, test.max, test.jitter); !errors.Is(err, ErrInvalidBackoff) {
			t.Fatalf("invalid policy accepted: %+v, err=%v", test, err)
		}
	}
	for _, test := range []struct {
		attempt                int
		base, max, extra, want time.Duration
	}{
		{2, 1, 10, 2, 6}, {2, 1, 10, 100, 10}, {3, 1, 8, 100, 8},
		{1, time.Duration(math.MaxInt64/2 + 1), time.Duration(math.MaxInt64), 0, time.Duration(math.MaxInt64)},
	} {
		got, err := CalculateBackoff(test.attempt, test.base, test.max, func(time.Duration) time.Duration { return test.extra })
		if err != nil || got != test.want {
			t.Fatalf("checked delay = %v, %v; want %v", got, err, test.want)
		}
	}
}

func TestBackoffHelpersBoundRetriesAndRejectInvalidPolicies(t *testing.T) {
	if CalculateBackoffByTime(-1, 1, 8) != 0 || CalculateBackoffByAttempt(0, 0, 2) != 0 || CalculateBackoffByAttempt(0, 1, -1) != 0 {
		t.Fatal("invalid retry policy returned a delay")
	}
	if got := CalculateBackoffByTime(0, 1000, 8000); got < 1000 || got > 1100 {
		t.Fatalf("jitter escaped range: %v", got)
	}
	if got := CalculateBackoffByAttempt(100, time.Second, 3); got != 8*time.Second {
		t.Fatalf("retry cap = %v", got)
	}
	if got := CalculateBackoffByAttempt(math.MaxInt, time.Second, math.MaxInt); got != time.Duration(math.MaxInt64) {
		t.Fatalf("maximum-attempt cap overflowed: %v", got)
	}
}
