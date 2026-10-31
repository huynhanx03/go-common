package unleash_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

func TestClientTargetingAndRollout(t *testing.T) {
	flags := []toggle.Flag{
		{
			Key: "targeting", Enabled: true,
			Rules: []toggle.Rule{
				{Rollout: 100, Constraints: []toggle.Constraint{
					{ContextName: "channel", Operator: "IN", Values: []string{"beta"}},
					{ContextName: "age", Operator: "NUM_GTE", Value: "18"},
				}},
				{Rollout: 100, Constraints: []toggle.Constraint{
					{ContextName: "region", Operator: "IN", Values: []string{"us"}},
				}},
			},
		},
		operatorFlag("op-in", toggle.Constraint{ContextName: "value", Operator: "IN", Values: []string{"match"}}),
		operatorFlag("op-not-in", toggle.Constraint{ContextName: "value", Operator: "NOT_IN", Values: []string{"match"}}),
		operatorFlag("op-contains", toggle.Constraint{ContextName: "value", Operator: "STR_CONTAINS", Values: []string{"match"}}),
		operatorFlag("op-starts", toggle.Constraint{ContextName: "value", Operator: "STR_STARTS_WITH", Values: []string{"match"}}),
		operatorFlag("op-ends", toggle.Constraint{ContextName: "value", Operator: "STR_ENDS_WITH", Values: []string{"match"}}),
		operatorFlag("op-eq", toggle.Constraint{ContextName: "value", Operator: "NUM_EQ", Value: "5"}),
		operatorFlag("op-lt", toggle.Constraint{ContextName: "value", Operator: "NUM_LT", Value: "5"}),
		operatorFlag("op-lte", toggle.Constraint{ContextName: "value", Operator: "NUM_LTE", Value: "5"}),
		operatorFlag("op-gt", toggle.Constraint{ContextName: "value", Operator: "NUM_GT", Value: "5"}),
		operatorFlag("op-gte", toggle.Constraint{ContextName: "value", Operator: "NUM_GTE", Value: "5"}),
		operatorFlag("case-insensitive", toggle.Constraint{ContextName: "value", Operator: "STR_CONTAINS", Values: []string{"beta"}, CaseInsensitive: true}),
		operatorFlag("inverted", toggle.Constraint{ContextName: "value", Operator: "IN", Values: []string{"allowed"}, Inverted: true}),
		rolloutFlag("zero", 0, "boundary-cohort", "userId"),
		rolloutFlag("all", 100, "boundary-cohort", "userId"),
		rolloutFlag("rollout-25", 25, "monotonic-cohort", "userId"),
		rolloutFlag("rollout-75", 75, "monotonic-cohort", "userId"),
		rolloutFlag("user-sticky", 50, "user-cohort", "userId"),
		rolloutFlag("session-sticky", 50, "session-cohort", "sessionId"),
		rolloutFlag("property-sticky", 50, "property-cohort", "tenantKey"),
		{
			Key: "ordered-variants", Enabled: true,
			Rules: []toggle.Rule{
				{Rollout: 100, GroupID: "first-rule", Stickiness: "userId", Constraints: []toggle.Constraint{{ContextName: "team", Operator: "IN", Values: []string{"blue"}}}, Variants: []toggle.Variant{{Name: "first", Weight: 1000, Payload: &toggle.Payload{Type: "string", Value: "first-payload"}}}},
				{Rollout: 100, GroupID: "second-rule", Stickiness: "userId", Constraints: []toggle.Constraint{{ContextName: "region", Operator: "IN", Values: []string{"west"}}}, Variants: []toggle.Variant{{Name: "second", Weight: 1000, Payload: &toggle.Payload{Type: "string", Value: "second-payload"}}}},
			},
		},
		{
			Key: "weighted", Enabled: true,
			Rules: []toggle.Rule{{Rollout: 100, GroupID: "weighted-cohort", Stickiness: "userId", Variants: []toggle.Variant{
				{Name: "control", Weight: 500, Payload: &toggle.Payload{Type: "json", Value: `{"arm":"control"}`}},
				{Name: "treatment", Weight: 500, Payload: &toggle.Payload{Type: "json", Value: `{"arm":"treatment"}`}},
			}}},
		},
	}
	client := newEvaluationClient(t, flags, nil)
	ctx := context.Background()

	checkEvaluation(t, client, "targeting", toggle.EvaluationContext{Properties: map[string]string{"channel": "beta", "age": "21", "region": "eu"}}, true)
	checkEvaluation(t, client, "targeting", toggle.EvaluationContext{Properties: map[string]string{"channel": "beta", "age": "17", "region": "eu"}}, false)
	checkEvaluation(t, client, "targeting", toggle.EvaluationContext{Properties: map[string]string{"channel": "stable", "age": "17", "region": "us"}}, true)
	checkEvaluation(t, client, "targeting", toggle.EvaluationContext{Properties: map[string]string{"channel": "stable", "age": "17", "region": "eu"}}, false)

	operators := []struct {
		key, match, nonmatch string
	}{
		{"op-in", "match", "other"},
		{"op-not-in", "other", "match"},
		{"op-contains", "prefix-match-suffix", "prefix-other-suffix"},
		{"op-starts", "match-suffix", "prefix-match"},
		{"op-ends", "prefix-match", "match-suffix"},
		{"op-eq", "5", "4"},
		{"op-lt", "4", "5"},
		{"op-lte", "5", "6"},
		{"op-gt", "6", "5"},
		{"op-gte", "5", "4"},
	}
	for _, operator := range operators {
		t.Run(operator.key, func(t *testing.T) {
			checkEvaluation(t, client, operator.key, toggle.EvaluationContext{Properties: map[string]string{"value": operator.match}}, true)
			checkEvaluation(t, client, operator.key, toggle.EvaluationContext{Properties: map[string]string{"value": operator.nonmatch}}, false)
		})
	}
	checkEvaluation(t, client, "case-insensitive", toggle.EvaluationContext{Properties: map[string]string{"value": "ALPHA-BETA"}}, true)
	checkEvaluation(t, client, "inverted", toggle.EvaluationContext{Properties: map[string]string{"value": "denied"}}, true)
	checkEvaluation(t, client, "inverted", toggle.EvaluationContext{Properties: map[string]string{"value": "allowed"}}, false)
	checkEvaluation(t, client, "zero", toggle.EvaluationContext{UserID: "user-1"}, false)
	checkEvaluation(t, client, "all", toggle.EvaluationContext{UserID: "user-1"}, true)
	if enabled, err := client.IsEnabled(ctx, "all", toggle.EvaluationContext{}, false); err != nil || enabled {
		t.Fatalf("userId stickiness without identity = %v, %v", enabled, err)
	}

	for i := 0; i < 300; i++ {
		user := fmt.Sprintf("user-%d", i)
		identity := toggle.EvaluationContext{UserID: user}
		quarter, err := client.IsEnabled(ctx, "rollout-25", identity, false)
		if err != nil {
			t.Fatal(err)
		}
		quarterAgain, err := client.IsEnabled(ctx, "rollout-25", identity, false)
		if err != nil || quarterAgain != quarter {
			t.Fatalf("25%% rollout was unstable for %q: %v %v", user, quarterAgain, err)
		}
		threeQuarters, err := client.IsEnabled(ctx, "rollout-75", identity, false)
		if err != nil || (quarter && !threeQuarters) {
			t.Fatalf("75%% rollout excluded a 25%% identity %q: %v %v", user, threeQuarters, err)
		}
	}
	assertSameDecision(t, client, "user-sticky", toggle.EvaluationContext{UserID: "same-user", SessionID: "session-a"}, toggle.EvaluationContext{UserID: "same-user", SessionID: "session-b"})
	assertSameDecision(t, client, "session-sticky", toggle.EvaluationContext{UserID: "user-a", SessionID: "same-session"}, toggle.EvaluationContext{UserID: "user-b", SessionID: "same-session"})
	assertSameDecision(t, client, "property-sticky", toggle.EvaluationContext{UserID: "user-a", Properties: map[string]string{"tenantKey": "tenant-a"}}, toggle.EvaluationContext{UserID: "user-b", Properties: map[string]string{"tenantKey": "tenant-a"}})
	checkEvaluation(t, client, "all", toggle.EvaluationContext{Properties: map[string]string{"irrelevant": "no identity"}}, true)

	ordered, err := client.GetVariant(ctx, "ordered-variants", toggle.EvaluationContext{UserID: "u1", Properties: map[string]string{"team": "blue", "region": "west"}})
	if err != nil || !ordered.Enabled || ordered.Name != "first" || ordered.Payload == nil || ordered.Payload.Value != "first-payload" {
		t.Fatalf("first matching rule variant = %+v, %v", ordered, err)
	}
	seen := map[string]bool{}
	var stableName string
	for i := 0; i < 300; i++ {
		user := fmt.Sprintf("variant-user-%d", i)
		variant, err := client.GetVariant(ctx, "weighted", toggle.EvaluationContext{UserID: user})
		if err != nil || !variant.Enabled || (variant.Name != "control" && variant.Name != "treatment") || variant.Payload == nil {
			t.Fatalf("weighted variant for %s = %+v, %v", user, variant, err)
		}
		seen[variant.Name] = true
		if variant.Name == "control" && variant.Payload.Value != `{"arm":"control"}` || variant.Name == "treatment" && variant.Payload.Value != `{"arm":"treatment"}` {
			t.Fatalf("payload does not match variant %q: %+v", variant.Name, variant.Payload)
		}
		if user == "variant-user-17" {
			stableName = variant.Name
			variant.Payload.Value = "mutated"
			again, err := client.GetVariant(ctx, "weighted", toggle.EvaluationContext{UserID: user})
			if err != nil || again.Name != stableName || again.Payload == nil || again.Payload.Value == "mutated" {
				t.Fatalf("variant identity or payload ownership changed: %+v, %v", again, err)
			}
		}
	}
	if !seen["control"] || !seen["treatment"] {
		t.Fatalf("weighted variants did not exercise both arms: %v", seen)
	}
}

func operatorFlag(key string, constraint toggle.Constraint) toggle.Flag {
	return toggle.Flag{Key: key, Enabled: true, Rules: []toggle.Rule{{Rollout: 100, Constraints: []toggle.Constraint{constraint}}}}
}

func rolloutFlag(key string, rollout int, group, stickiness string) toggle.Flag {
	return toggle.Flag{Key: key, Enabled: true, Rules: []toggle.Rule{{Rollout: rollout, GroupID: group, Stickiness: stickiness}}}
}

func newEvaluationClient(t *testing.T, flags []toggle.Flag, onError func(error)) toggle.Client {
	t.Helper()
	snapshot := toggle.Snapshot{Scope: testScope, Revision: 1, Flags: flags}
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: testScope, AppName: "evaluation-test", Source: sourceFunc(func(context.Context, toggle.Scope) (toggle.Snapshot, error) { return snapshot, nil }),
		RefreshInterval: time.Hour, SourceTimeout: time.Second, OnError: onError,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return client
}

func checkEvaluation(t *testing.T, client toggle.Client, key string, evaluation toggle.EvaluationContext, want bool) {
	t.Helper()
	if evaluation.UserID == "" {
		evaluation.UserID = "evaluation-user"
	}
	got, err := client.IsEnabled(context.Background(), key, evaluation, false)
	if err != nil || got != want {
		t.Fatalf("IsEnabled(%s)=%v err=%v want=%v context=%+v", key, got, err, want, evaluation)
	}
}

func assertSameDecision(t *testing.T, client toggle.Client, key string, left, right toggle.EvaluationContext) {
	t.Helper()
	a, err := client.IsEnabled(context.Background(), key, left, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.IsEnabled(context.Background(), key, right, false)
	if err != nil || a != b {
		t.Fatalf("stickiness %q differs for equivalent identity: %v vs %v (%v)", key, a, b, err)
	}
}
