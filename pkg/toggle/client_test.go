package toggle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/toggle"
)

type sourceFunc func(context.Context, toggle.Scope) (toggle.Snapshot, error)

func (f sourceFunc) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	return f(ctx, scope)
}

func TestNewRejectsInvalidSettings(t *testing.T) {
	valid := settings.Toggle{Namespace: "billing", Environment: settings.EnvProd, AppName: "billing-api"}
	validSource := sourceFunc(func(context.Context, toggle.Scope) (toggle.Snapshot, error) {
		return toggle.Snapshot{}, nil
	})
	badInterval := valid
	badInterval.RefreshIntervalSeconds = -1
	badTimeout := valid
	badTimeout.SourceTimeoutSeconds = -1
	badAppName := valid
	badAppName.AppName = "billing api"
	badEnvironment := valid
	badEnvironment.Environment = "prod!"
	largeSeconds := int(^uint(0) >> 1)
	badRefreshOverflow := valid
	badRefreshOverflow.RefreshIntervalSeconds = largeSeconds
	badTimeoutOverflow := valid
	badTimeoutOverflow.SourceTimeoutSeconds = largeSeconds
	var typedNilSource sourceFunc
	cases := []struct {
		name   string
		config settings.Toggle
		source toggle.Source
	}{
		{name: "namespace", config: settings.Toggle{Environment: valid.Environment, AppName: valid.AppName}, source: validSource},
		{name: "environment", config: settings.Toggle{Namespace: valid.Namespace, AppName: valid.AppName}, source: validSource},
		{name: "invalid environment", config: badEnvironment, source: validSource},
		{name: "app name", config: settings.Toggle{Namespace: valid.Namespace, Environment: valid.Environment}, source: validSource},
		{name: "invalid app name", config: badAppName, source: validSource},
		{name: "negative refresh interval", config: badInterval, source: validSource},
		{name: "negative source timeout", config: badTimeout, source: validSource},
		{name: "refresh duration overflow", config: badRefreshOverflow, source: validSource},
		{name: "source timeout duration overflow", config: badTimeoutOverflow, source: validSource},
		{name: "source", config: valid},
		{name: "typed nil source", config: valid, source: typedNilSource},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, err := toggle.New(context.Background(), test.config, test.source)
			if client != nil || !errors.Is(err, toggle.ErrInvalidConfig) {
				t.Fatalf("New() = (%v, %v), want nil client and ErrInvalidConfig", client, err)
			}
		})
	}
}

func TestNewRejectsMissingOrCanceledContextBeforeLoading(t *testing.T) {
	var loads int
	source := sourceFunc(func(context.Context, toggle.Scope) (toggle.Snapshot, error) {
		loads++
		return toggle.Snapshot{}, nil
	})
	config := settings.Toggle{Namespace: "billing", Environment: settings.EnvProd, AppName: "billing-api"}
	if client, err := toggle.New(nil, config, source); client != nil || !errors.Is(err, toggle.ErrInvalidContext) {
		t.Fatalf("New(nil context) = (%v, %v)", client, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if client, err := toggle.New(canceled, config, source); client != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("New(canceled context) = (%v, %v)", client, err)
	}
	if loads != 0 {
		t.Fatalf("invalid contexts reached Source.Load %d times", loads)
	}
}

func TestNewTimesOutDuringSourceBootstrap(t *testing.T) {
	config := settings.Toggle{
		Namespace: "billing", Environment: settings.EnvProd, AppName: "billing-api", SourceTimeoutSeconds: 1,
	}
	source := sourceFunc(func(ctx context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
		<-ctx.Done()
		return toggle.Snapshot{}, ctx.Err()
	})

	client, err := toggle.New(context.Background(), config, source)
	if client != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New() = (%v, %v), want nil client and deadline exceeded", client, err)
	}
}

func TestNewEvaluatesFromConfiguredSource(t *testing.T) {
	config := settings.Toggle{Namespace: "billing", Environment: settings.EnvProd, AppName: "billing-api", SourceTimeoutSeconds: 1}
	source := sourceFunc(func(_ context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
		return toggle.Snapshot{Scope: scope, Revision: 1, Flags: []toggle.Flag{{Key: "checkout", Enabled: true}}}, nil
	})

	client, err := toggle.New(context.Background(), config, source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	enabled, err := client.IsEnabled(context.Background(), "checkout", toggle.EvaluationContext{}, false)
	if err != nil || !enabled {
		t.Fatalf("IsEnabled() = (%v, %v), want (true, nil)", enabled, err)
	}
}

func TestNewUsesDefaultRefreshAndSourceTimeout(t *testing.T) {
	config := settings.Toggle{Namespace: "billing", Environment: settings.EnvProd, AppName: "billing-api"}
	client, err := toggle.New(context.Background(), config, sourceFunc(func(_ context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
		return toggle.Snapshot{Scope: scope, Revision: 1, Flags: []toggle.Flag{{Key: "checkout", Enabled: true}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}
