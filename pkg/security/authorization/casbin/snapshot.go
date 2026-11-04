package casbin

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	casbinlib "github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
)

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
	// Subjects are request-scoped and high-cardinality. Do not retain g() cache
	// entries for ephemeral IDs across authorization evaluations.
	enforcer.EnableGFunctionCache(false)
	groupRelations := newRelationRoleManager(enforcer.GetNamedRoleManager(policyTypeGrouping))
	if groupRelations != nil {
		enforcer.SetNamedRoleManager(policyTypeGrouping, groupRelations)
	}
	resourceRelations := newRelationRoleManager(enforcer.GetNamedRoleManager(policyTypeResource))
	if resourceRelations != nil {
		enforcer.SetNamedRoleManager(policyTypeResource, resourceRelations)
	}
	scopeParents, err := scopeHierarchy(candidate.Rules)
	if err != nil {
		return nil, err
	}
	if _, exists := casbinModel[policyTypeGrouping][policyTypeScope]; exists {
		matches := func(child, parent string) bool {
			return scopeDescendsFrom(scopeParents, child, parent)
		}
		enforcer.AddNamedDomainMatchingFunc(policyTypeGrouping, scopeMatcherName, matches)
		enforcer.AddFunction(scopeFunctionName, func(arguments ...interface{}) (interface{}, error) {
			if len(arguments) != 2 {
				return false, ErrInvalidSnapshot
			}
			child, childOK := arguments[0].(string)
			parent, parentOK := arguments[1].(string)
			if !childOK || !parentOK {
				return false, ErrInvalidSnapshot
			}
			return matches(child, parent), nil
		})
	}

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
		case policyTypePolicy:
			added, err = enforcer.AddNamedPolicy(rule.Type, values)
		case policyTypeGrouping:
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
		revision:          candidate.Revision,
		requestArity:      len(casbinModel[requestModelSection][requestModelSection].Tokens),
		enforcer:          enforcer,
		groupRelations:    groupRelations,
		resourceRelations: resourceRelations,
	}, nil
}

func validateModelShape(casbinModel model.Model) error {
	policyAssertions, ok := casbinModel[policyModelSection]
	if !ok || len(policyAssertions) == 0 {
		return ErrInvalidSnapshot
	}
	requestAssertions, ok := casbinModel[requestModelSection]
	request, requestOK := requestAssertions[requestModelSection]
	if !ok || !requestOK || (len(request.Tokens) != 3 && len(request.Tokens) != 4 && len(request.Tokens) != 5) {
		return ErrInvalidSnapshot
	}
	for _, section := range []string{policyTypePolicy, policyTypeGrouping} {
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

func scopeHierarchy(rules []PolicyRule) (map[string][]string, error) {
	parents := make(map[string][]string)
	seen := make(map[[2]string]struct{})
	for _, rule := range rules {
		if rule.Type != policyTypeScope {
			continue
		}
		if rule.V0 == "" || rule.V1 == "" || rule.V0 == rule.V1 ||
			rule.V2 != "" || rule.V3 != "" || rule.V4 != "" || rule.V5 != "" {
			return nil, ErrInvalidSnapshot
		}
		pair := [2]string{rule.V0, rule.V1}
		if _, exists := seen[pair]; exists {
			return nil, ErrInvalidSnapshot
		}
		seen[pair] = struct{}{}
		parents[rule.V0] = append(parents[rule.V0], rule.V1)
	}
	state := make(map[string]uint8)
	depths := make(map[string]int)
	var longestPath func(string) (int, error)
	longestPath = func(node string) (int, error) {
		if state[node] == 1 {
			return 0, ErrInvalidSnapshot
		}
		if state[node] == 2 {
			return depths[node], nil
		}
		state[node] = 1
		longest := 0
		for _, parent := range parents[node] {
			parentDepth, err := longestPath(parent)
			if err != nil {
				return 0, err
			}
			if parentDepth+1 > longest {
				longest = parentDepth + 1
			}
		}
		if longest > DefaultMaxScopeHierarchyDepth {
			return 0, ErrInvalidSnapshot
		}
		state[node] = 2
		depths[node] = longest
		return longest, nil
	}
	for child := range parents {
		if _, err := longestPath(child); err != nil {
			return nil, err
		}
	}
	return parents, nil
}

func scopeDescendsFrom(parents map[string][]string, child, ancestor string) bool {
	if child == ancestor {
		return true
	}
	visited := make(map[string]struct{})
	pending := append([]string(nil), parents[child]...)
	for len(pending) != 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if current == ancestor {
			return true
		}
		if _, exists := visited[current]; exists {
			continue
		}
		visited[current] = struct{}{}
		pending = append(pending, parents[current]...)
	}
	return false
}
