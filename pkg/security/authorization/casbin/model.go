package casbin

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	casbinlib "github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
)

const maxPolicyTypeBytes = 64

func prepareLiveSnapshot(
	ctx context.Context,
	candidate Snapshot,
	limits Limits,
) (*liveSnapshot, error) {
	if ctx == nil {
		return nil, ErrInvalidSnapshot
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if candidate.Revision <= 0 ||
		candidate.Model == "" ||
		len(candidate.Model) > limits.MaxModelBytes ||
		len(candidate.Rules) > limits.MaxRules {
		return nil, ErrInvalidSnapshot
	}

	copiedRules := append([]PolicyRule(nil), candidate.Rules...)
	casbinModel, err := model.NewModelFromString(candidate.Model)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	if err := validateModelShape(casbinModel); err != nil {
		return nil, err
	}
	enforcer, err := casbinlib.NewEnforcer(casbinModel)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	enforcer.EnableAutoSave(false)
	enforcer.EnableAutoBuildRoleLinks(false)

	for _, rule := range copiedRules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		section, values, err := validatePolicyRule(
			casbinModel,
			rule,
			limits.MaxValueBytes,
		)
		if err != nil {
			return nil, ErrInvalidSnapshot
		}
		var added bool
		switch section {
		case "p":
			added, err = enforcer.AddNamedPolicy(rule.Type, values)
		case "g":
			added, err = enforcer.AddNamedGroupingPolicy(rule.Type, values)
		default:
			return nil, ErrInvalidSnapshot
		}
		if err != nil || !added {
			return nil, ErrInvalidSnapshot
		}
	}
	if err := enforcer.BuildRoleLinks(); err != nil {
		return nil, ErrInvalidSnapshot
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &liveSnapshot{
		revision: candidate.Revision,
		enforcer: enforcer,
	}, nil
}

func validateModelShape(casbinModel model.Model) error {
	policyAssertions, ok := casbinModel["p"]
	if !ok || len(policyAssertions) == 0 {
		return ErrInvalidSnapshot
	}
	for _, section := range []string{"p", "g"} {
		for _, assertion := range casbinModel[section] {
			if len(assertion.Tokens) == 0 || len(assertion.Tokens) > 6 {
				return ErrInvalidSnapshot
			}
		}
	}
	return nil
}

func validatePolicyRule(
	casbinModel model.Model,
	rule PolicyRule,
	maxValueBytes int,
) (string, []string, error) {
	if err := validatePolicyType(rule.Type); err != nil {
		return "", nil, err
	}
	section := rule.Type[:1]
	assertions, ok := casbinModel[section]
	if !ok {
		return "", nil, ErrInvalidSnapshot
	}
	assertion, ok := assertions[rule.Type]
	if !ok {
		return "", nil, ErrInvalidSnapshot
	}
	arity := len(assertion.Tokens)
	if arity == 0 || arity > 6 {
		return "", nil, ErrInvalidSnapshot
	}

	allValues := rule.values()
	values := make([]string, arity)
	for index := range allValues {
		value := allValues[index]
		if index >= arity {
			if value != "" {
				return "", nil, ErrInvalidSnapshot
			}
			continue
		}
		if err := validatePolicyValue(value, maxValueBytes); err != nil {
			return "", nil, err
		}
		values[index] = value
	}
	return section, values, nil
}

func validatePolicyType(value string) error {
	if len(value) == 0 ||
		len(value) > maxPolicyTypeBytes ||
		(value[0] != 'p' && value[0] != 'g') {
		return ErrInvalidSnapshot
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_') {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func validatePolicyValue(value string, maximum int) error {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return ErrInvalidSnapshot
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: policy value", ErrInvalidSnapshot)
		}
	}
	return nil
}
