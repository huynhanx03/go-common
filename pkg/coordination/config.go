package coordination

import (
	"math"
	"time"

	"github.com/huynhanx03/go-common/pkg/algorithm"
	"github.com/huynhanx03/go-common/pkg/settings"
)

type runtimeConfig struct {
	ttl              time.Duration
	renewInterval    time.Duration
	operationTimeout time.Duration
	cleanupTimeout   time.Duration
	acquireTimeout   time.Duration
	backoff          algorithm.Backoff
	backoffSettings  settings.Backoff
}

func normalizeConfig(config settings.Coordination) (runtimeConfig, error) {
	ttl, err := durationOrDefault(config.TTLMilliseconds, defaultLeaseTTL)
	if err != nil {
		return runtimeConfig{}, err
	}

	renewInterval, err := durationFromMilliseconds(config.RenewIntervalMilliseconds)
	if err != nil {
		return runtimeConfig{}, err
	}
	if renewInterval == 0 {
		renewInterval = (ttl / 3).Truncate(time.Millisecond)
	}
	if renewInterval <= 0 || renewInterval >= ttl {
		return runtimeConfig{}, ErrInvalidConfig
	}

	operationTimeout, err := durationFromMilliseconds(config.OperationTimeoutMilliseconds)
	if err != nil {
		return runtimeConfig{}, err
	}
	if operationTimeout == 0 {
		operationTimeout = min(defaultLeaseOperationTimeout, renewInterval/2).Truncate(time.Millisecond)
	}
	if operationTimeout <= 0 || operationTimeout >= renewInterval {
		return runtimeConfig{}, ErrInvalidConfig
	}

	cleanupTimeout, err := durationOrDefault(config.CleanupTimeoutMilliseconds, defaultLeaseCleanupTimeout)
	if err != nil {
		return runtimeConfig{}, ErrInvalidConfig
	}
	acquireTimeout, err := durationFromMilliseconds(config.AcquireTimeoutMilliseconds)
	if err != nil {
		return runtimeConfig{}, err
	}
	backoffSettings, backoff, err := normalizeBackoff(config.Backoff)
	if err != nil {
		return runtimeConfig{}, err
	}

	return runtimeConfig{
		ttl:              ttl,
		renewInterval:    renewInterval,
		operationTimeout: operationTimeout,
		cleanupTimeout:   cleanupTimeout,
		acquireTimeout:   acquireTimeout,
		backoff:          backoff,
		backoffSettings:  backoffSettings,
	}, nil
}

func normalizeBackoff(config settings.Backoff) (settings.Backoff, algorithm.Backoff, error) {
	if config.InitialDelayMilliseconds == 0 {
		config.InitialDelayMilliseconds = int(defaultBackoffInitialDelay / time.Millisecond)
	}
	if config.MaxDelayMilliseconds == 0 {
		config.MaxDelayMilliseconds = int(defaultBackoffMaxDelay / time.Millisecond)
	}
	if config.Multiplier == 0 {
		config.Multiplier = defaultBackoffMultiplier
	}
	if config.Multiplier <= 1 || math.IsNaN(config.Multiplier) || math.IsInf(config.Multiplier, 0) {
		return settings.Backoff{}, nil, ErrInvalidConfig
	}

	initialDelay, err := durationFromMilliseconds(config.InitialDelayMilliseconds)
	if err != nil || initialDelay <= 0 {
		return settings.Backoff{}, nil, ErrInvalidConfig
	}
	maxDelay, err := durationFromMilliseconds(config.MaxDelayMilliseconds)
	if err != nil || maxDelay < initialDelay {
		return settings.Backoff{}, nil, ErrInvalidConfig
	}

	backoff := algorithm.NewJitterBackoff(algorithm.NewExponentialBackoff(initialDelay, maxDelay, config.Multiplier))
	return config, backoff, nil
}

func durationOrDefault(milliseconds int, fallback time.Duration) (time.Duration, error) {
	duration, err := durationFromMilliseconds(milliseconds)
	if err != nil || duration == 0 {
		return fallback, err
	}
	return duration, nil
}

func durationFromMilliseconds(milliseconds int) (time.Duration, error) {
	if milliseconds < 0 || int64(milliseconds) > maxDurationMilliseconds {
		return 0, ErrInvalidConfig
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func (config runtimeConfig) toSettings() settings.Coordination {
	return settings.Coordination{
		TTLMilliseconds:              int(config.ttl / time.Millisecond),
		RenewIntervalMilliseconds:    int(config.renewInterval / time.Millisecond),
		OperationTimeoutMilliseconds: int(config.operationTimeout / time.Millisecond),
		CleanupTimeoutMilliseconds:   int(config.cleanupTimeout / time.Millisecond),
		AcquireTimeoutMilliseconds:   int(config.acquireTimeout / time.Millisecond),
		Backoff:                      config.backoffSettings,
	}
}
