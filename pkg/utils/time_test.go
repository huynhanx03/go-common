package utils

import (
	"testing"
	"time"
)

func TestJitterDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		fraction float64
		sample   float64
		want     time.Duration
	}{
		{name: "minimum jitter", duration: 10 * time.Second, fraction: 0.1, sample: 0, want: 9 * time.Second},
		{name: "no jitter", duration: 10 * time.Second, fraction: 0.1, sample: 0.5, want: 10 * time.Second},
		{name: "maximum jitter", duration: 10 * time.Second, fraction: 0.1, sample: 1, want: 11 * time.Second},
		{name: "disabled", duration: 10 * time.Second, fraction: 0, sample: 0.25, want: 10 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := JitterDuration(test.duration, test.fraction, test.sample); got != test.want {
				t.Fatalf("JitterDuration() = %v, want %v", got, test.want)
			}
		})
	}
}
