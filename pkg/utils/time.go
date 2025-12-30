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
