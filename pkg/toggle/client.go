package toggle

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

// Client evaluates flags locally within its bound scope.
type Client = core.Client

// New creates a locally evaluating client backed by the service's snapshot source.
func New(ctx context.Context, config settings.Toggle, source Source, options ...ClientOption) (Client, error) {
	resolved := resolveClientOptions(options)
	return unleash.New(ctx, config, source, resolved.onError)
}
