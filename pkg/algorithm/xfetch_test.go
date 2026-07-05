package algorithm

import (
	"testing"
	"time"
)

func TestXFetchShouldRefresh(t *testing.T) {
	// Far from expiry with an instant recompute → probability ~0.
	for i := 0; i < 100; i++ {
		if XFetchShouldRefresh(time.Now().Add(time.Hour), 0, 1.0) {
			t.Fatal("refreshed an instant-to-compute value an hour before expiry")
		}
	}
	// Past expiry → always refresh.
	for i := 0; i < 100; i++ {
		if !XFetchShouldRefresh(time.Now().Add(-time.Millisecond), time.Millisecond, 1.0) {
			t.Fatal("did not refresh an already-expired value")
		}
	}
}
