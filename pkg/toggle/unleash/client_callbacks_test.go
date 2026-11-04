package unleash

import (
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

func TestListenerForwardsClassifiedSourceFailures(t *testing.T) {
	cause := errors.New("private database detail")
	forwarded := make(chan error, 2)
	events := &listener{onError: func(err error) { forwarded <- err }}
	events.OnError(cause)
	events.OnWarning(cause)
	for range 2 {
		err := <-forwarded
		if !errors.Is(err, core.ErrSource) || !errors.Is(err, cause) {
			t.Fatalf("listener error = %v, want source category and cause", err)
		}
		if strings.Contains(err.Error(), cause.Error()) {
			t.Fatalf("listener exposed source details: %v", err)
		}
	}
	(&listener{}).OnWarning(cause)
}
