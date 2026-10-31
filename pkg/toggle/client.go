package toggle

import "context"

// Client evaluates flags locally within its bound scope.
type Client interface {
	IsEnabled(context.Context, string, EvaluationContext, bool) (bool, error)
	GetVariant(context.Context, string, EvaluationContext) (VariantResult, error)
	Close(context.Context) error
}
