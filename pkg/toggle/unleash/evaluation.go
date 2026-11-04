package unleash

import (
	"context"

	sdk "github.com/Unleash/unleash-go-sdk/v6"
	sdkcontext "github.com/Unleash/unleash-go-sdk/v6/context"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

func validateEvaluation(ctx context.Context, key string, evaluation core.EvaluationContext) error {
	if ctx == nil {
		return core.ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := core.Identifier(key, core.MaxIdentifierLength, core.ErrInvalidContext); err != nil {
		return err
	}
	if len(evaluation.UserID) > core.MaxContextValueLength || len(evaluation.SessionID) > core.MaxContextValueLength || len(evaluation.RemoteAddress) > core.MaxContextValueLength || len(evaluation.Properties) > core.MaxEvaluationProperties {
		return core.LimitError(core.ErrInvalidContext)
	}
	for name, value := range evaluation.Properties {
		if err := core.Identifier(name, core.MaxPropertyNameLength, core.ErrInvalidContext); err != nil {
			return err
		}
		switch name {
		case "userId", "sessionId", "remoteAddress", "appName", "environment":
			return core.ErrInvalidContext
		}
		if len(value) > core.MaxContextValueLength {
			return core.LimitError(core.ErrInvalidContext)
		}
	}
	return nil
}

func (c *client) IsEnabled(ctx context.Context, key string, evaluation core.EvaluationContext, fallback bool) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return fallback, core.ErrClosed
	}
	if err := validateEvaluation(ctx, key, evaluation); err != nil {
		return fallback, err
	}
	return c.engine.IsEnabled(key, sdk.FeatureOptions{Ctx: mapContext(evaluation), Fallback: &fallback}), nil
}

func (c *client) GetVariant(ctx context.Context, key string, evaluation core.EvaluationContext) (core.VariantResult, error) {
	disabled := core.VariantResult{Name: "disabled"}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return disabled, core.ErrClosed
	}
	if err := validateEvaluation(ctx, key, evaluation); err != nil {
		return disabled, err
	}

	variant := c.engine.GetVariant(key, sdk.VariantOptions{Ctx: mapContext(evaluation)})
	result := core.VariantResult{Name: variant.Name, Enabled: variant.Enabled, FeatureEnabled: variant.FeatureEnabled}
	if variant.Payload.Type != "" {
		result.Payload = &core.Payload{Type: variant.Payload.Type, Value: variant.Payload.Value}
	}
	return result, nil
}

func mapContext(evaluation core.EvaluationContext) sdkcontext.Context {
	properties := make(map[string]string, len(evaluation.Properties))
	for k, v := range evaluation.Properties {
		properties[k] = v
	}
	return sdkcontext.Context{UserId: evaluation.UserID, SessionId: evaluation.SessionID, RemoteAddress: evaluation.RemoteAddress, Properties: properties}
}
