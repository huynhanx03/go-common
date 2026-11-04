package unleash

import (
	"context"
	"errors"
	"testing"

	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

func TestNewClientRejectsInvalidContextAndConfigurationBeforeLoading(t *testing.T) {
	scope := core.Scope{Namespace: "billing", Environment: "test"}
	loads := 0
	config := clientConfig{
		scope: scope, appName: "billing-api", source: testSourceFunc(func(context.Context, core.Scope) (core.Snapshot, error) {
			loads++
			return core.Snapshot{Scope: scope, Revision: 1}, nil
		}),
	}
	if client, err := newClient(nil, config); client != nil || !errors.Is(err, core.ErrInvalidContext) {
		t.Fatalf("newClient(nil context) = (%v, %v)", client, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if client, err := newClient(canceled, config); client != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("newClient(canceled context) = (%v, %v)", client, err)
	}
	invalid := config
	invalid.source = nil
	if client, err := newClient(context.Background(), invalid); client != nil || !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("newClient(invalid config) = (%v, %v)", client, err)
	}
	if loads != 0 {
		t.Fatalf("invalid inputs loaded source %d times", loads)
	}
}
