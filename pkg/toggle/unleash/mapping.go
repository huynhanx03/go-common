package unleash

import (
	"strconv"

	"github.com/Unleash/unleash-go-sdk/v6/api"
	sdkcontext "github.com/Unleash/unleash-go-sdk/v6/context"
	"github.com/huynhanx03/go-common/pkg/toggle"
)

func mapSnapshot(snapshot toggle.Snapshot) *api.FeatureResponse {
	response := &api.FeatureResponse{Features: make([]api.Feature, 0, len(snapshot.Flags)), Segments: []api.Segment{}}
	for _, flag := range snapshot.Flags {
		feature := api.Feature{Name: flag.Key, Description: flag.Description, Enabled: flag.Enabled}
		for _, rule := range flag.Rules {
			strategy := api.Strategy{Name: "flexibleRollout", Parameters: api.ParameterMap{"rollout": strconv.Itoa(rule.Rollout), "groupId": rule.GroupID, "stickiness": rule.Stickiness}}
			for _, constraint := range rule.Constraints {
				strategy.Constraints = append(strategy.Constraints, api.Constraint{ContextName: constraint.ContextName, Operator: api.Operator(constraint.Operator), Value: constraint.Value, Values: append([]string(nil), constraint.Values...), CaseInsensitive: constraint.CaseInsensitive, Inverted: constraint.Inverted})
			}
			for _, variant := range rule.Variants {
				mapped := api.VariantInternal{Variant: api.Variant{Name: variant.Name}, Weight: variant.Weight, WeightType: "fix", Stickiness: rule.Stickiness}
				if variant.Payload != nil {
					mapped.Payload = api.Payload{Type: variant.Payload.Type, Value: variant.Payload.Value}
				}
				strategy.Variants = append(strategy.Variants, mapped)
			}
			feature.Strategies = append(feature.Strategies, strategy)
		}
		response.Features = append(response.Features, feature)
	}
	return response
}

func mapContext(evaluation toggle.EvaluationContext) sdkcontext.Context {
	properties := make(map[string]string, len(evaluation.Properties))
	for k, v := range evaluation.Properties {
		properties[k] = v
	}
	return sdkcontext.Context{UserId: evaluation.UserID, SessionId: evaluation.SessionID, RemoteAddress: evaluation.RemoteAddress, Properties: properties}
}
