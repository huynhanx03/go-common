package utils

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/constraints"
)

// ToDuration converts any integer type (seconds) to time.Duration.
func ToDuration[T constraints.Integer](seconds T) time.Duration {
	return time.Duration(seconds) * time.Second
}

// ToDurationMs converts any integer type (milliseconds) to time.Duration.
func ToDurationMs[T constraints.Integer](milliseconds T) time.Duration {
	return time.Duration(milliseconds) * time.Millisecond
}

// JitterDuration applies symmetric multiplicative jitter using a sample in [0, 1].
func JitterDuration(duration time.Duration, fraction, sample float64) time.Duration {
	return JitterDurationRange(duration, fraction, fraction, sample)
}

// JitterDurationRange applies uniform multiplicative jitter using a sample in [0, 1].
func JitterDurationRange(duration time.Duration, lowerFraction, upperFraction, sample float64) time.Duration {
	factor := 1 - lowerFraction + (lowerFraction+upperFraction)*sample
	return time.Duration(float64(duration) * factor)
}
