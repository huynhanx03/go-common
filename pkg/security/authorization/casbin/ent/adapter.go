// Package ent provides the Ent schema and read-only Casbin adapter over a dialect
// driver. It never creates schema or writes policies; applications persist
// changes through their own transaction-aware repositories.
package ent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/casbin/casbin/v3/model"
	"github.com/casbin/casbin/v3/persist"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	DefaultTableName    = "casbin_rules"
	DefaultPolicyLimit  = 100_000
	HardPolicyLimit     = 1_000_000
	MaxPolicyValueBytes = 4096
	MaxFilterValues     = 10_000
)

var (
	ErrInvalidOption    = errors.New("invalid option")
	ErrPolicyLimit      = errors.New("policy limit exceeded")
	ErrReadOnly         = errors.New("writes must use the application transaction")
	ErrInvalidPolicyRow = errors.New("invalid policy row")
)

type Adapter struct {
	driver   dialect.Driver
	table    string
	limit    int
	filtered bool
}

// NewAdapter binds to an Ent driver and its policy-loading settings. The caller
// owns driver lifecycle, migrations, transaction boundaries, and policy writes.
func NewAdapter(driver dialect.Driver, config settings.Casbin) (*Adapter, error) {
	if driver == nil || driver.Dialect() == "" {
		return nil, ErrInvalidOption
	}
	table := config.TableName
	if table == "" {
		table = DefaultTableName
	}
	if !validIdentifier(table) {
		return nil, ErrInvalidOption
	}
	limit := config.PolicyLimit
	if limit == 0 {
		limit = DefaultPolicyLimit
	}
	if limit < 0 || limit > HardPolicyLimit {
		return nil, ErrInvalidOption
	}
	return &Adapter{driver: driver, table: table, limit: limit}, nil
}

func (a *Adapter) LoadPolicy(policyModel model.Model) error {
	return a.LoadPolicyContext(context.Background(), policyModel)
}

// LoadPolicyContext loads a complete, bounded policy snapshot.
func (a *Adapter) LoadPolicyContext(ctx context.Context, policyModel model.Model) error {
	return a.load(ctx, policyModel, nil)
}

// Filter matches the canonical Casbin adapter fields. Each non-empty slice is
// an allow-list for that column; columns are combined with AND semantics.
type Filter struct {
	Ptype                []string
	V0                   []string
	V1                   []string
	V2                   []string
	V3                   []string
	V4                   []string
	V5                   []string
	Scope                *authorization.Scope
	Ancestors            []string
	RelatedResources     []string
	GroupSubjects        []string
	GroupSubjectPrefixes []string
	ResourceSubjects     []string
}

// ScopeFilter selects permission rows for the requested scope, role
// assignments at that scope plus the explicit system scope, and g2 relationship
// rows for the supplied trusted resource keys.
func ScopeFilter(scope authorization.Scope, ancestors []authorization.Scope, relatedResources ...string) (Filter, error) {
	if err := scope.Validate(); err != nil {
		return Filter{}, ErrInvalidOption
	}
	ancestorKeys := make([]string, 0, len(ancestors))
	seenAncestors := map[string]struct{}{scope.Key(): {}}
	for _, ancestor := range ancestors {
		if err := ancestor.Validate(); err != nil {
			return Filter{}, ErrInvalidOption
		}
		key := ancestor.Key()
		if _, exists := seenAncestors[key]; exists {
			return Filter{}, ErrInvalidOption
		}
		seenAncestors[key] = struct{}{}
		ancestorKeys = append(ancestorKeys, key)
	}
	for _, resource := range relatedResources {
		if resource == "" || len(resource) > 512 || strings.TrimSpace(resource) != resource || strings.ContainsAny(resource, "\r\n\x00") {
			return Filter{}, ErrInvalidOption
		}
	}
	return Filter{Scope: &scope, Ancestors: ancestorKeys, RelatedResources: append([]string(nil), relatedResources...)}, nil
}

func (a *Adapter) LoadFilteredPolicy(policyModel model.Model, filter any) error {
	return a.LoadFilteredPolicyContext(context.Background(), policyModel, filter)
}

// LoadFilteredPolicyContext loads a bounded policy snapshot for the given filter.
func (a *Adapter) LoadFilteredPolicyContext(ctx context.Context, policyModel model.Model, filter any) error {
	value, ok := filter.(Filter)
	if !ok {
		return ErrInvalidOption
	}
	a.filtered = true
	return a.load(ctx, policyModel, &value)
}

func (a *Adapter) IsFiltered() bool { return a != nil && a.filtered }

func (a *Adapter) load(ctx context.Context, policyModel model.Model, filter *Filter) error {
	if a == nil || a.driver == nil || ctx == nil || policyModel == nil {
		return ErrInvalidOption
	}
	query := entsql.Dialect(a.driver.Dialect()).
		Select("ptype", "v0", "v1", "v2", "v3", "v4", "v5").
		From(entsql.Table(a.table)).
		OrderBy(entsql.Asc("ptype"), entsql.Asc("v0"), entsql.Asc("v1"), entsql.Asc("v2"), entsql.Asc("v3"), entsql.Asc("v4"), entsql.Asc("v5")).
		Limit(a.limit + 1)
	if filter != nil {
		if err := filter.validate(); err != nil {
			return err
		}
		predicate := filter.predicate()
		if predicate != nil {
			query.Where(predicate)
		}
	}
	sqlText, args := query.Query()
	var rows entsql.Rows
	if err := a.driver.Query(ctx, sqlText, args, &rows); err != nil {
		return fmt.Errorf("query policy rows: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > a.limit {
			return ErrPolicyLimit
		}
		var values [7]string
		destinations := []any{&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]}
		if err := rows.Scan(destinations...); err != nil {
			return fmt.Errorf("scan policy row: %w", err)
		}
		if values[0] == "" || len(values[0]) > 64 || strings.ContainsAny(values[0], "\r\n\x00") {
			return ErrInvalidPolicyRow
		}
		for _, value := range values[1:] {
			if len(value) > MaxPolicyValueBytes || strings.ContainsAny(value, "\r\n\x00") {
				return ErrInvalidPolicyRow
			}
		}
		section := values[0][:1]
		assertions, sectionOK := policyModel[section]
		assertion, assertionOK := assertions[values[0]]
		if !sectionOK || !assertionOK || len(assertion.Tokens) == 0 || len(assertion.Tokens) > 6 {
			return ErrInvalidPolicyRow
		}
		arity := len(assertion.Tokens)
		for _, unused := range values[1+arity:] {
			if unused != "" {
				return ErrInvalidPolicyRow
			}
		}
		if err := persist.LoadPolicyArray(values[:1+arity], policyModel); err != nil {
			return fmt.Errorf("load policy row: %w", errors.Join(ErrInvalidPolicyRow, err))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read policy rows: %w", err)
	}
	return nil
}

func (f Filter) predicate() *entsql.Predicate {
	fields := []struct {
		column string
		values []string
	}{
		{"ptype", f.Ptype}, {"v0", f.V0}, {"v1", f.V1}, {"v2", f.V2},
		{"v3", f.V3}, {"v4", f.V4}, {"v5", f.V5},
	}
	result := make([]*entsql.Predicate, 0, len(fields)+1)
	for _, field := range fields {
		if len(field.values) == 0 {
			continue
		}
		values := make([]any, len(field.values))
		for index, value := range field.values {
			values[index] = value
		}
		result = append(result, entsql.In(field.column, values...))
	}
	if f.Scope != nil {
		scopeKey := f.Scope.Key()
		domains := append([]string{scopeKey}, f.Ancestors...)
		policyDomains := append(append([]string(nil), domains...), "*")
		roleDomains := append(append([]string(nil), domains...), authorization.DefaultSystemScope().Key())
		groupRelation := []*entsql.Predicate{
			entsql.EQ("ptype", "g"),
			stringIn("v2", roleDomains),
		}
		groupSubjects := make([]*entsql.Predicate, 0, 2)
		if len(f.GroupSubjects) != 0 {
			groupSubjects = append(groupSubjects, stringIn("v0", f.GroupSubjects))
		}
		for _, prefix := range f.GroupSubjectPrefixes {
			groupSubjects = append(groupSubjects, entsql.Like("v0", prefix+"%"))
		}
		if len(groupSubjects) != 0 {
			groupRelation = append(groupRelation, entsql.Or(groupSubjects...))
		}
		groups := []*entsql.Predicate{
			entsql.And(entsql.EQ("ptype", "p"), stringIn("v1", policyDomains)),
			entsql.And(groupRelation...),
			entsql.And(entsql.EQ("ptype", "g3"), stringIn("v0", domains)),
		}
		if len(f.RelatedResources) != 0 {
			values := make([]any, len(f.RelatedResources))
			for index, value := range f.RelatedResources {
				values[index] = value
			}
			resourceRelation := []*entsql.Predicate{
				entsql.EQ("ptype", "g2"),
				entsql.In("v2", values...),
			}
			if len(f.ResourceSubjects) != 0 {
				resourceRelation = append(resourceRelation, stringIn("v0", f.ResourceSubjects))
			}
			groups = append(groups, entsql.And(resourceRelation...))
		}
		result = append(result, entsql.Or(groups...))
	}
	if len(result) == 0 {
		return nil
	}
	return entsql.And(result...)
}

func (f Filter) validate() error {
	fields := [][]string{
		f.Ptype, f.V0, f.V1, f.V2, f.V3, f.V4, f.V5,
		f.Ancestors, f.RelatedResources, f.GroupSubjects,
		f.GroupSubjectPrefixes, f.ResourceSubjects,
	}
	for _, values := range fields {
		if len(values) > MaxFilterValues {
			return ErrInvalidOption
		}
		for _, value := range values {
			if len(value) > MaxPolicyValueBytes || strings.ContainsAny(value, "\r\n\x00") {
				return ErrInvalidOption
			}
		}
	}
	for _, prefix := range f.GroupSubjectPrefixes {
		if strings.ContainsAny(prefix, "%_\\") {
			return ErrInvalidOption
		}
	}
	if f.Scope != nil {
		if err := f.Scope.Validate(); err != nil {
			return ErrInvalidOption
		}
	}
	return nil
}

func stringIn(column string, values []string) *entsql.Predicate {
	arguments := make([]any, len(values))
	for index, value := range values {
		arguments[index] = value
	}
	return entsql.In(column, arguments...)
}

func (a *Adapter) SavePolicy(model.Model) error                              { return ErrReadOnly }
func (a *Adapter) AddPolicy(string, string, []string) error                  { return ErrReadOnly }
func (a *Adapter) RemovePolicy(string, string, []string) error               { return ErrReadOnly }
func (a *Adapter) RemoveFilteredPolicy(string, string, int, ...string) error { return ErrReadOnly }

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

var _ persist.Adapter = (*Adapter)(nil)
var _ persist.FilteredAdapter = (*Adapter)(nil)
