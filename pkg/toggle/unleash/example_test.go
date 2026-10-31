package unleash_test

import (
	"context"
	"fmt"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

type exampleSource struct{ snapshot toggle.Snapshot }

func (s exampleSource) Load(ctx context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return toggle.Snapshot{}, err
	}
	return s.snapshot, nil
}

func ExampleNew() {
	ctx := context.Background()
	scope := toggle.Scope{Namespace: "billing", Environment: "production"}
	source := exampleSource{snapshot: toggle.Snapshot{
		Scope: scope, Revision: 1,
		Flags: []toggle.Flag{{
			Key: "checkout-v2", Enabled: true,
			Rules: []toggle.Rule{{
				Rollout:     100,
				Constraints: []toggle.Constraint{{ContextName: "region", Operator: "IN", Values: []string{"us"}}},
				Variants:    []toggle.Variant{{Name: "new-checkout", Weight: 1000, Payload: &toggle.Payload{Type: "json", Value: `{"layout":"compact"}`}}},
			}},
		}},
	}}
	client, err := unleash.New(ctx, unleash.Config{Scope: scope, AppName: "billing-api", Source: source, RefreshInterval: time.Hour})
	if err != nil {
		fmt.Println("client startup failed")
		return
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer client.Close(closeCtx)

	usContext := toggle.EvaluationContext{UserID: "user-42", Properties: map[string]string{"region": "us"}}
	newCheckout, err := client.IsEnabled(ctx, "checkout-v2", usContext, false)
	if err != nil {
		fmt.Println("evaluation failed")
		return
	}
	fallback, err := client.IsEnabled(ctx, "unknown-flag", usContext, true)
	if err != nil {
		fmt.Println("fallback evaluation failed")
		return
	}
	variant, err := client.GetVariant(ctx, "checkout-v2", usContext)
	if err != nil {
		fmt.Println("variant evaluation failed")
		return
	}
	fmt.Printf("targeted: %v, fallback: %v, variant: %s (%s)\n", newCheckout, fallback, variant.Name, variant.Payload.Value)
	// Output:
	// targeted: true, fallback: true, variant: new-checkout ({"layout":"compact"})
}
