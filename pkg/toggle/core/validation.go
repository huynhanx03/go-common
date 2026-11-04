package core

import (
	"math"
	"sort"
	"strconv"

	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
)

func identifier(value string, limit int, category error) error {
	if len(value) > limit {
		return invalidLimit(category)
	}
	if value == "" {
		return category
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-') {
			return category
		}
	}
	return nil
}

// ValidateScope checks the explicitly supplied namespace and environment.
func ValidateScope(scope Scope) error {
	if err := identifier(scope.Namespace, MaxIdentifierLength, ErrInvalidConfig); err != nil {
		return err
	}
	return identifier(scope.Environment, MaxIdentifierLength, ErrInvalidConfig)
}

// ValidateExpectedRevision checks that a revision can be advanced safely.
func ValidateExpectedRevision(revision int64) error {
	if revision < 0 || revision == math.MaxInt64 {
		return ErrInvalidDefinition
	}
	return nil
}

// NormalizeFlag validates a definition and returns a deep owned copy.
func NormalizeFlag(flag Flag) (Flag, error) {
	if err := identifier(flag.Key, MaxIdentifierLength, ErrInvalidDefinition); err != nil {
		return Flag{}, err
	}
	if len(flag.Description) > MaxFlagDescriptionLength || len(flag.Rules) > MaxRulesPerFlag {
		return Flag{}, invalidLimit(ErrInvalidDefinition)
	}
	// Bound raw input before allocating copies or the JSON codec's buffer. The
	// encoded size is checked exactly below; strings may expand during encoding.
	budget := len(flag.Key) + len(flag.Description)
	for _, rule := range flag.Rules {
		if len(rule.Constraints) > MaxConstraintsPerRule || len(rule.Variants) > MaxVariantsPerRule {
			return Flag{}, invalidLimit(ErrInvalidDefinition)
		}
		if rule.Rollout < 0 || rule.Rollout > MaxRolloutPercent {
			return Flag{}, ErrInvalidDefinition
		}
		if rule.GroupID != "" {
			if err := identifier(rule.GroupID, MaxIdentifierLength, ErrInvalidDefinition); err != nil {
				return Flag{}, err
			}
		}
		if rule.Stickiness != "" {
			if err := identifier(rule.Stickiness, MaxPropertyNameLength, ErrInvalidDefinition); err != nil {
				return Flag{}, err
			}
		}
		budget += len(rule.GroupID) + len(rule.Stickiness)
		for _, constraint := range rule.Constraints {
			if err := identifier(constraint.ContextName, MaxPropertyNameLength, ErrInvalidDefinition); err != nil {
				return Flag{}, err
			}
			if len(constraint.Values) > MaxValuesPerConstraint || len(constraint.Value) > MaxConstraintValueLength {
				return Flag{}, invalidLimit(ErrInvalidDefinition)
			}
			budget += len(constraint.ContextName) + len(constraint.Operator) + len(constraint.Value)
			switch constraint.Operator {
			case "IN", "NOT_IN", "STR_CONTAINS", "STR_STARTS_WITH", "STR_ENDS_WITH":
				if len(constraint.Values) == 0 || constraint.Value != "" {
					return Flag{}, ErrInvalidDefinition
				}
			case "NUM_EQ", "NUM_LT", "NUM_LTE", "NUM_GT", "NUM_GTE":
				if len(constraint.Values) != 0 || constraint.CaseInsensitive {
					return Flag{}, ErrInvalidDefinition
				}
				value, err := strconv.ParseFloat(constraint.Value, 64)
				if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
					return Flag{}, ErrInvalidDefinition
				}
			default:
				return Flag{}, ErrInvalidDefinition
			}
			for _, value := range constraint.Values {
				if len(value) > MaxConstraintValueLength {
					return Flag{}, invalidLimit(ErrInvalidDefinition)
				}
				budget += len(value) + 3
			}
			if budget > MaxDefinitionSize {
				return Flag{}, invalidLimit(ErrInvalidDefinition)
			}
		}
		names := make(map[string]struct{}, len(rule.Variants))
		total := 0
		for _, variant := range rule.Variants {
			if err := identifier(variant.Name, MaxIdentifierLength, ErrInvalidDefinition); err != nil {
				return Flag{}, err
			}
			if _, ok := names[variant.Name]; ok {
				return Flag{}, ErrInvalidDefinition
			}
			names[variant.Name] = struct{}{}
			if variant.Weight < 0 || variant.Weight > VariantWeightScale {
				return Flag{}, ErrInvalidDefinition
			}
			total += variant.Weight
			budget += len(variant.Name)
			if variant.Payload != nil {
				budget += len(variant.Payload.Type) + len(variant.Payload.Value)
				if budget > MaxDefinitionSize {
					return Flag{}, invalidLimit(ErrInvalidDefinition)
				}
				switch variant.Payload.Type {
				case "string":
				case "json":
					if !jsoncodec.Valid([]byte(variant.Payload.Value)) {
						return Flag{}, ErrInvalidDefinition
					}
				default:
					return Flag{}, ErrInvalidDefinition
				}
			}
		}
		if len(rule.Variants) > 0 && total != VariantWeightScale {
			return Flag{}, ErrInvalidDefinition
		}
		if budget > MaxDefinitionSize {
			return Flag{}, invalidLimit(ErrInvalidDefinition)
		}
	}
	normalized := flag
	normalized.Rules = nil
	if len(flag.Rules) > 0 {
		normalized.Rules = make([]Rule, len(flag.Rules))
	}
	for i, rule := range flag.Rules {
		if rule.GroupID == "" {
			rule.GroupID = flag.Key
		}
		if rule.Stickiness == "" {
			rule.Stickiness = "userId"
		}
		rule.Constraints = nil
		rule.Variants = nil
		for _, constraint := range flag.Rules[i].Constraints {
			constraint.Values = append([]string(nil), constraint.Values...)
			rule.Constraints = append(rule.Constraints, constraint)
		}
		for _, variant := range flag.Rules[i].Variants {
			if variant.Payload != nil {
				payload := *variant.Payload
				variant.Payload = &payload
			}
			rule.Variants = append(rule.Variants, variant)
		}
		normalized.Rules[i] = rule
	}
	encoded, err := jsoncodec.Marshal(normalized)
	if err != nil {
		return Flag{}, ErrInvalidDefinition
	}
	if len(encoded) > MaxDefinitionSize {
		return Flag{}, invalidLimit(ErrInvalidDefinition)
	}
	return normalized, nil
}

// NormalizeSnapshot validates a complete scoped candidate before publication.
// Flag order is canonical; rule and variant order remains significant.
func NormalizeSnapshot(scope Scope, snapshot Snapshot) (Snapshot, error) {
	if err := ValidateScope(scope); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Scope != scope || snapshot.Revision < 0 || (snapshot.Revision == 0 && len(snapshot.Flags) != 0) {
		return Snapshot{}, ErrInvalidDefinition
	}
	if len(snapshot.Flags) > MaxFlagsPerSnapshot {
		return Snapshot{}, invalidLimit(ErrInvalidDefinition)
	}
	normalized := Snapshot{Scope: scope, Revision: snapshot.Revision}
	keys := make(map[string]struct{}, len(snapshot.Flags))
	size := 0
	for _, flag := range snapshot.Flags {
		if _, ok := keys[flag.Key]; ok {
			return Snapshot{}, ErrInvalidDefinition
		}
		keys[flag.Key] = struct{}{}
		copied, err := NormalizeFlag(flag)
		if err != nil {
			return Snapshot{}, err
		}
		encoded, _ := jsoncodec.Marshal(copied)
		size += len(encoded) + 1
		if size > MaxSnapshotSize {
			return Snapshot{}, invalidLimit(ErrInvalidDefinition)
		}
		normalized.Flags = append(normalized.Flags, copied)
	}
	sort.Slice(normalized.Flags, func(i, j int) bool { return normalized.Flags[i].Key < normalized.Flags[j].Key })
	encoded, _ := jsoncodec.Marshal(normalized)
	if len(encoded) > MaxSnapshotSize {
		return Snapshot{}, invalidLimit(ErrInvalidDefinition)
	}
	return normalized, nil
}

// Identifier checks an identifier against a byte limit and error category.
func Identifier(value string, limit int, category error) error {
	return identifier(value, limit, category)
}
