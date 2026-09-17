package request

import (
	"testing"
	"time"
)

func FuzzParseRetryAfter(f *testing.F) {
	for _, seed := range []string{"", "0", "10", "-1", "Wed, 21 Oct 2015 07:28:00 GMT", "token=secret"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		delay, ok := parseRetryAfter(value, time.Unix(1_700_000_000, 0))
		if ok && delay < 0 {
			t.Fatalf("negative retry delay: %v", delay)
		}
	})
}
