package unleash

import (
	"context"
	"math"
	"time"

	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

type clientConfig struct {
	scope           core.Scope
	appName         string
	source          core.Source
	refreshInterval time.Duration
	sourceTimeout   time.Duration
	onError         func(error)
}

func configFromSettings(config settings.Toggle, source core.Source, onError func(error)) (clientConfig, error) {
	refreshInterval, err := secondsDuration(config.RefreshIntervalSeconds)
	if err != nil {
		return clientConfig{}, err
	}
	sourceTimeout, err := secondsDuration(config.SourceTimeoutSeconds)
	if err != nil {
		return clientConfig{}, err
	}

	return clientConfig{
		scope:   core.Scope{Namespace: config.Namespace, Environment: string(config.Environment)},
		appName: config.AppName, source: source, refreshInterval: refreshInterval,
		sourceTimeout: sourceTimeout, onError: onError,
	}, nil
}

func secondsDuration(seconds int) (time.Duration, error) {
	if seconds < 0 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return 0, core.ErrInvalidConfig
	}
	return time.Duration(seconds) * time.Second, nil
}

func normalizeConfig(config clientConfig) (clientConfig, error) {
	if err := core.ValidateScope(config.scope); err != nil {
		return clientConfig{}, err
	}
	if err := core.Identifier(config.appName, core.MaxIdentifierLength, core.ErrInvalidConfig); err != nil {
		return clientConfig{}, err
	}
	if core.IsNil(config.source) || config.refreshInterval < 0 || config.sourceTimeout < 0 {
		return clientConfig{}, core.ErrInvalidConfig
	}
	if config.refreshInterval == 0 {
		config.refreshInterval = defaultRefreshInterval
	}
	if config.sourceTimeout == 0 {
		config.sourceTimeout = defaultSourceTimeout
	}
	return config, nil
}

func loadInitialSnapshot(ctx context.Context, config clientConfig) (core.Snapshot, error) {
	loadCtx, cancel := context.WithTimeout(ctx, config.sourceTimeout)
	defer cancel()

	snapshot, err := config.source.Load(loadCtx, config.scope)
	if err == nil {
		err = loadCtx.Err()
	}
	if err != nil {
		return core.Snapshot{}, err
	}
	return core.NormalizeSnapshot(config.scope, snapshot)
}
