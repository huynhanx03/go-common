package coordination

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

// Client coordinates advisory leases.
type Client struct {
	core *clientCore
}

// FencedClient coordinates leases that issue fencing grants.
type FencedClient struct {
	core *clientCore
}

type clientCore struct {
	config  runtimeConfig
	log     *logger.Logger
	fenced  bool
	acquire func(context.Context, AcquireRequest) (Lease, Grant, error)
}

// New creates a client over an adapter that provides advisory ownership only.
func New(adapter Adapter, log *logger.Logger, config settings.Coordination) (*Client, error) {
	if adapter == nil || log == nil {
		return nil, ErrInvalidConfig
	}
	core, err := newClientCore(log, config, false)
	if err != nil {
		return nil, err
	}
	core.acquire = func(ctx context.Context, request AcquireRequest) (Lease, Grant, error) {
		lease, err := adapter.TryAcquire(ctx, request)
		return lease, Grant{}, err
	}
	return &Client{core: core}, nil
}

// NewFenced creates a client that requires a valid fence from every acquisition.
func NewFenced(adapter FencedAdapter, log *logger.Logger, config settings.Coordination) (*FencedClient, error) {
	if adapter == nil || log == nil {
		return nil, ErrInvalidConfig
	}
	core, err := newClientCore(log, config, true)
	if err != nil {
		return nil, err
	}
	core.acquire = func(ctx context.Context, request AcquireRequest) (Lease, Grant, error) {
		lease, err := adapter.TryAcquireFenced(ctx, request)
		if err != nil || lease == nil {
			return lease, Grant{}, err
		}
		return lease, lease.Grant(), nil
	}
	return &FencedClient{core: core}, nil
}

func newClientCore(log *logger.Logger, config settings.Coordination, fenced bool) (*clientCore, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	return &clientCore{config: normalized, log: log, fenced: fenced}, nil
}

// Config returns the normalized settings used by the client.
func (c *Client) Config() settings.Coordination { return c.core.config.toSettings() }

// Config returns the normalized settings used by the client.
func (c *FencedClient) Config() settings.Coordination { return c.core.config.toSettings() }
