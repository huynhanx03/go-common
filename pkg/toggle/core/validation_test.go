package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
)

func TestNormalizeSnapshotSortsAndOwnsDefinitions(t *testing.T) {
	scope := Scope{Namespace: "billing", Environment: "prod"}
	input := Snapshot{
		Scope: scope, Revision: 3,
		Flags: []Flag{{
			Key: "z-last",
			Rules: []Rule{{
				Rollout:     100,
				Constraints: []Constraint{{ContextName: "plan", Operator: "IN", Values: []string{"pro"}}},
				Variants:    []Variant{{Name: "v2", Weight: 1000, Payload: &Payload{Type: "string", Value: "new"}}},
			}},
		}, {Key: "a-first"}},
	}

	got, err := NormalizeSnapshot(scope, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags[0].Key != "a-first" || got.Flags[1].Key != "z-last" {
		t.Fatalf("flags are not canonical: %+v", got.Flags)
	}

	input.Flags[0].Rules[0].Constraints[0].Values[0] = "changed"
	input.Flags[0].Rules[0].Variants[0].Payload.Value = "changed"
	if got.Flags[1].Rules[0].Constraints[0].Values[0] != "pro" || got.Flags[1].Rules[0].Variants[0].Payload.Value != "new" {
		t.Fatalf("normalized snapshot aliases caller memory: %+v", got.Flags[1])
	}
}

func TestNormalizeSnapshotRejectsDuplicateKeys(t *testing.T) {
	scope := Scope{Namespace: "billing", Environment: "prod"}
	_, err := NormalizeSnapshot(scope, Snapshot{
		Scope: scope, Revision: 1,
		Flags: []Flag{{Key: "checkout"}, {Key: "checkout"}},
	})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("NormalizeSnapshot error = %v, want ErrInvalidDefinition", err)
	}
}

func TestNormalizeFlagRejectsInvalidRulesAndResourceLimits(t *testing.T) {
	base := func() Flag { return Flag{Key: "checkout"} }
	makeValues := func(count, size int) []string {
		values := make([]string, count)
		for i := range values {
			values[i] = strings.Repeat("x", size)
		}
		return values
	}

	cases := []struct {
		name string
		flag Flag
		want error
	}{
		{name: "too many constraints", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: make([]Constraint, MaxConstraintsPerRule+1)}}
			return f
		}(), want: ErrLimit},
		{name: "too many variants", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: make([]Variant, MaxVariantsPerRule+1)}}
			return f
		}(), want: ErrLimit},
		{name: "invalid group id", flag: func() Flag { f := base(); f.Rules = []Rule{{Rollout: 100, GroupID: "bad id"}}; return f }(), want: ErrInvalidDefinition},
		{name: "invalid stickiness", flag: func() Flag { f := base(); f.Rules = []Rule{{Rollout: 100, Stickiness: "bad name"}}; return f }(), want: ErrInvalidDefinition},
		{name: "rollout outside range", flag: func() Flag { f := base(); f.Rules = []Rule{{Rollout: MaxRolloutPercent + 1}}; return f }(), want: ErrInvalidDefinition},
		{name: "invalid constraint property", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "bad name", Operator: "IN", Values: []string{"pro"}}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "oversized scalar constraint value", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "plan", Operator: "NUM_EQ", Value: strings.Repeat("1", MaxConstraintValueLength+1)}}}}
			return f
		}(), want: ErrLimit},
		{name: "string operator with scalar value", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "plan", Operator: "IN", Value: "pro", Values: []string{"pro"}}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "empty string operator values", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "plan", Operator: "IN"}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "malformed numeric operand", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "age", Operator: "NUM_EQ", Value: "not-a-number"}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "numeric constraint with list values", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "age", Operator: "NUM_EQ", Value: "18", Values: []string{"18"}}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "numeric constraint case insensitive", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "age", Operator: "NUM_EQ", Value: "18", CaseInsensitive: true}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "oversized list value", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "plan", Operator: "IN", Values: []string{strings.Repeat("x", MaxConstraintValueLength+1)}}}}}
			return f
		}(), want: ErrLimit},
		{name: "invalid variant name", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "bad name", Weight: VariantWeightScale}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "negative variant weight", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: -1}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "variant weight above scale", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: VariantWeightScale + 1}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "duplicate variant names", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "same", Weight: 500}, {Name: "same", Weight: 500}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "variant weights do not total scale", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: VariantWeightScale - 1}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "invalid JSON payload", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: VariantWeightScale, Payload: &Payload{Type: "json", Value: "{"}}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "unsupported payload type", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: VariantWeightScale, Payload: &Payload{Type: "binary", Value: "x"}}}}}
			return f
		}(), want: ErrInvalidDefinition},
		{name: "payload exceeds encoded definition budget", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Variants: []Variant{{Name: "v1", Weight: VariantWeightScale, Payload: &Payload{Type: "string", Value: strings.Repeat("x", MaxDefinitionSize)}}}}}
			return f
		}(), want: ErrLimit},
		{name: "constraint and variants exceed definition budget", flag: func() Flag {
			f := base()
			values := makeValues(64, MaxConstraintValueLength)
			values[len(values)-1] = strings.Repeat("x", 800)
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "p", Operator: "IN", Values: values}}, Variants: []Variant{{Name: strings.Repeat("v", MaxIdentifierLength), Weight: VariantWeightScale}}}}
			return f
		}(), want: ErrLimit},
		{name: "JSON escaping exceeds definition budget", flag: func() Flag {
			f := base()
			values := makeValues(63, MaxConstraintValueLength)
			for i := range values {
				values[i] = strings.Repeat("\x00", MaxConstraintValueLength)
			}
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "p", Operator: "IN", Values: values}}}}
			return f
		}(), want: ErrLimit},
		{name: "cumulative definition budget", flag: func() Flag {
			f := base()
			f.Rules = []Rule{{Rollout: 100, Constraints: []Constraint{{ContextName: "p", Operator: "IN", Values: makeValues(65, 1024)}}}}
			return f
		}(), want: ErrLimit},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeFlag(test.flag); !errors.Is(err, ErrInvalidDefinition) || !errors.Is(err, test.want) {
				t.Fatalf("NormalizeFlag() error = %v, want invalid definition and %v", err, test.want)
			}
		})
	}
}

func TestNormalizeSnapshotRejectsInvalidScopeAndRevision(t *testing.T) {
	scope := Scope{Namespace: "billing", Environment: "prod"}
	cases := []struct {
		name     string
		requested Scope
		snapshot Snapshot
	}{
		{name: "invalid requested scope", requested: Scope{Environment: "prod"}, snapshot: Snapshot{Scope: scope, Revision: 1}},
		{name: "source scope mismatch", requested: scope, snapshot: Snapshot{Scope: Scope{Namespace: "other", Environment: "prod"}, Revision: 1}},
		{name: "negative revision", requested: scope, snapshot: Snapshot{Scope: scope, Revision: -1}},
		{name: "flags without a revision", requested: scope, snapshot: Snapshot{Scope: scope, Flags: []Flag{{Key: "checkout"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeSnapshot(test.requested, test.snapshot); !errors.Is(err, ErrInvalidDefinition) && !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NormalizeSnapshot() error = %v, want invalid scope or definition", err)
			}
		})
	}
}

func TestNormalizeSnapshotRejectsFlagAndSnapshotSizeLimits(t *testing.T) {
	scope := Scope{Namespace: "billing", Environment: "prod"}
	tooMany := make([]Flag, MaxFlagsPerSnapshot+1)
	for i := range tooMany {
		tooMany[i] = Flag{Key: fmt.Sprintf("flag-%d", i)}
	}
	if _, err := NormalizeSnapshot(scope, Snapshot{Scope: scope, Revision: 1, Flags: tooMany}); !errors.Is(err, ErrLimit) {
		t.Fatalf("flag-count limit error = %v, want ErrLimit", err)
	}

	large := make([]Flag, 0, 4200)
	total := 0
	for i := 0; i < cap(large); i++ {
		flag := Flag{Key: fmt.Sprintf("flag-%05d", i), Description: strings.Repeat("x", MaxFlagDescriptionLength)}
		encoded, err := jsoncodec.Marshal(flag)
		if err != nil {
			t.Fatal(err)
		}
		large = append(large, flag)
		total += len(encoded) + 1
		if total > MaxSnapshotSize {
			break
		}
	}
	if _, err := NormalizeSnapshot(scope, Snapshot{Scope: scope, Revision: 1, Flags: large}); !errors.Is(err, ErrLimit) {
		t.Fatalf("cumulative snapshot limit error = %v, want ErrLimit", err)
	}

	for len(large) > 0 {
		last := large[len(large)-1]
		encoded, err := jsoncodec.Marshal(last)
		if err != nil {
			t.Fatal(err)
		}
		remaining := MaxSnapshotSize - total
		if remaining >= len(encoded)+1+64 {
			break
		}
		total -= len(encoded) + 1
		large = large[:len(large)-1]
	}
	var key string
	var emptyTail Flag
	var descriptionLength int
	for {
		key = fmt.Sprintf("flag-%05d", len(large))
		emptyTail = Flag{Key: key}
		emptyEncoded, err := jsoncodec.Marshal(emptyTail)
		if err != nil {
			t.Fatal(err)
		}
		descriptionLength = MaxSnapshotSize - 32 - total - len(emptyEncoded) - 1
		if descriptionLength <= MaxFlagDescriptionLength {
			break
		}
		large = append(large, emptyTail)
		total += len(emptyEncoded) + 1
	}
	if descriptionLength < 0 || descriptionLength > MaxFlagDescriptionLength {
		t.Fatalf("could not construct snapshot metadata boundary: tail description length %d", descriptionLength)
	}
	large = append(large, Flag{Key: key, Description: strings.Repeat("x", descriptionLength)})
	snapshot := Snapshot{Scope: scope, Revision: 1, Flags: large}
	if lenEncodedFlags(t, snapshot.Flags) != MaxSnapshotSize-32 {
		t.Fatal("test snapshot is not at the intended aggregate byte boundary")
	}
	encodedSnapshot, err := jsoncodec.Marshal(snapshot)
	if err != nil || len(encodedSnapshot) <= MaxSnapshotSize {
		t.Fatalf("test snapshot size = %d, error = %v; want metadata to exceed limit", len(encodedSnapshot), err)
	}
	if _, err := NormalizeSnapshot(scope, snapshot); !errors.Is(err, ErrLimit) {
		t.Fatalf("serialized snapshot limit error = %v, want ErrLimit", err)
	}
}

func lenEncodedFlags(t *testing.T, flags []Flag) int {
	t.Helper()
	total := 0
	for _, flag := range flags {
		encoded, err := jsoncodec.Marshal(flag)
		if err != nil {
			t.Fatal(err)
		}
		total += len(encoded) + 1
	}
	return total
}
