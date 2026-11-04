package ent

import (
	"context"
	stdsql "database/sql"
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/casbin/casbin/v3/model"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
	"github.com/huynhanx03/go-common/pkg/settings"
)

const adapterTestModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

func TestAdapterLoadsBoundedPoliciesAndAppliesScopeFilters(t *testing.T) {
	t.Parallel()

	driver := &policyDriver{dialect: dialect.Postgres, rows: [][]string{
		{"p", "role:reader", "document", "read", "", "", ""},
		{"g", "user:1", "role:reader", "", "", "", ""},
	}}
	adapter := newAdapterForTest(t, driver, settings.Casbin{TableName: "tenant_policy", PolicyLimit: 2})
	parsedModel := newAdapterTestModel(t)
	if err := adapter.LoadPolicy(parsedModel); err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	policies, err := parsedModel.GetPolicy("p", "p")
	if err != nil || len(policies) != 1 || policies[0][0] != "role:reader" || policies[0][2] != "read" {
		t.Fatalf("loaded policies = %#v, %v", policies, err)
	}
	if !driver.rowsClosed {
		t.Fatal("LoadPolicy() left the result rows open")
	}
	if !strings.Contains(driver.query, "tenant_policy") || !strings.Contains(driver.query, "ORDER BY") {
		t.Fatalf("policy query does not target configured table in stable order: %s", driver.query)
	}

	driver.rows = [][]string{{"p", "role:reader", "document", "read", "", "", ""}}
	filter, err := ScopeFilter(
		authorization.Scope{Type: "tenant", ID: "acme"},
		[]authorization.Scope{{Type: "organization", ID: "north"}},
		"contest:42",
	)
	if err != nil {
		t.Fatal(err)
	}
	filter.GroupSubjects = []string{"user:1"}
	filter.GroupSubjectPrefixes = []string{"service"}
	filter.ResourceSubjects = []string{"team:1"}
	filter.V0 = []string{"role:reader"}
	if err := adapter.LoadFilteredPolicy(parsedModel, filter); err != nil {
		t.Fatalf("LoadFilteredPolicy() error = %v", err)
	}
	if !adapter.IsFiltered() {
		t.Fatal("IsFiltered() = false after filtered load")
	}
	if !strings.Contains(driver.query, "OR") || !strings.Contains(driver.query, "AND") {
		t.Fatalf("scope filter did not compose allow-lists with scope relationships: %s", driver.query)
	}
	if strings.Contains(driver.query, "user:1") || !containsString(driver.args, "user:1") {
		t.Fatalf("filter values were not passed as query arguments: query=%q args=%#v", driver.query, driver.args)
	}

	driver.rows = nil
	if err := adapter.LoadFilteredPolicyContext(context.Background(), newAdapterTestModel(t), Filter{}); err != nil {
		t.Fatalf("LoadFilteredPolicyContext(empty filter) error = %v", err)
	}
	if strings.Contains(driver.query, "WHERE") {
		t.Fatalf("empty filter unexpectedly restricted the complete policy load: %s", driver.query)
	}
}

func TestAdapterRejectsInvalidConfigurationAndReadOnlyWrites(t *testing.T) {
	t.Parallel()

	driver := &policyDriver{dialect: dialect.Postgres}
	if _, err := NewAdapter(nil, settings.Casbin{}); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("NewAdapter(nil driver) error = %v", err)
	}
	if _, err := NewAdapter(&policyDriver{}, settings.Casbin{}); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("NewAdapter(empty dialect) error = %v", err)
	}
	for _, config := range []settings.Casbin{
		{TableName: "9policies"},
		{TableName: "policy; DROP TABLE users"},
		{TableName: "policy-name"},
		{PolicyLimit: -1},
		{PolicyLimit: HardPolicyLimit + 1},
	} {
		if _, err := NewAdapter(driver, config); !errors.Is(err, ErrInvalidOption) {
			t.Errorf("NewAdapter(%#v) error = %v", config, err)
		}
	}

	adapter := newAdapterForTest(t, driver, settings.Casbin{})
	policyModel := newAdapterTestModel(t)
	writeOperations := []struct {
		name string
		call func() error
	}{
		{name: "save", call: func() error { return adapter.SavePolicy(policyModel) }},
		{name: "add", call: func() error { return adapter.AddPolicy("p", "p", []string{"role", "document", "read"}) }},
		{name: "remove", call: func() error { return adapter.RemovePolicy("p", "p", []string{"role", "document", "read"}) }},
		{name: "remove filtered", call: func() error { return adapter.RemoveFilteredPolicy("p", "p", 0, "role") }},
	}
	for _, operation := range writeOperations {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			if err := operation.call(); !errors.Is(err, ErrReadOnly) {
				t.Fatalf("write operation error = %v", err)
			}
		})
	}
}

func TestAdapterBoundsAndValidatesRowsBeforeLoadingPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rows     [][]string
		limit    int
		queryErr error
		scanErr  error
		rowsErr  error
		wantErr  error
	}{
		{name: "policy limit", rows: [][]string{
			{"p", "role:reader", "document", "read", "", "", ""},
			{"p", "role:writer", "document", "write", "", "", ""},
		}, limit: 1, wantErr: ErrPolicyLimit},
		{name: "query failure", queryErr: errQueryFailure, wantErr: errQueryFailure},
		{name: "scan failure", rows: [][]string{{"p", "role:reader", "document", "read", "", "", ""}}, scanErr: errScanFailure, wantErr: errScanFailure},
		{name: "row iteration failure", rowsErr: errRowsFailure, wantErr: errRowsFailure},
		{name: "empty policy type", rows: [][]string{{"", "role", "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "unknown policy type", rows: [][]string{{"p9", "role", "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "control in policy type", rows: [][]string{{"p\n", "role", "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "oversized policy value", rows: [][]string{{"p", strings.Repeat("x", MaxPolicyValueBytes+1), "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "control in policy value", rows: [][]string{{"p", "role\nreader", "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "extra value after assertion", rows: [][]string{{"p", "role", "document", "read", "extra", "", ""}}, wantErr: ErrInvalidPolicyRow},
		{name: "unknown assertion", rows: [][]string{{"p2", "role", "document", "read", "", "", ""}}, wantErr: ErrInvalidPolicyRow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			driver := &policyDriver{
				dialect:  dialect.Postgres,
				rows:     test.rows,
				queryErr: test.queryErr,
				scanErr:  test.scanErr,
				rowsErr:  test.rowsErr,
			}
			limit := test.limit
			if limit == 0 {
				limit = 10
			}
			adapter := newAdapterForTest(t, driver, settings.Casbin{PolicyLimit: limit})
			err := adapter.LoadPolicyContext(context.Background(), newAdapterTestModel(t))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("LoadPolicyContext() error = %v, want %v", err, test.wantErr)
			}
			if !driver.rowsClosed && test.queryErr == nil {
				t.Fatal("LoadPolicyContext() did not close result rows")
			}
		})
	}
}

func TestScopeFilterValidatesAndCopiesCallerInput(t *testing.T) {
	t.Parallel()

	ancestors := []authorization.Scope{{Type: "organization", ID: "north"}}
	resources := []string{"contest:1"}
	filter, err := ScopeFilter(authorization.Scope{Type: "tenant", ID: "acme"}, ancestors, resources...)
	if err != nil {
		t.Fatal(err)
	}
	ancestors[0].ID = "mutated"
	resources[0] = "mutated"
	if filter.Ancestors[0] != "organization:north" || filter.RelatedResources[0] != "contest:1" {
		t.Fatalf("ScopeFilter retained caller-owned slices: %#v", filter)
	}

	tests := []struct {
		name      string
		scope     authorization.Scope
		ancestors []authorization.Scope
		resources []string
	}{
		{name: "invalid scope"},
		{name: "invalid ancestor", scope: authorization.Scope{Type: "tenant", ID: "acme"}, ancestors: []authorization.Scope{{Type: "Tenant", ID: "north"}}},
		{name: "duplicate ancestor", scope: authorization.Scope{Type: "tenant", ID: "acme"}, ancestors: []authorization.Scope{{Type: "tenant", ID: "acme"}}},
		{name: "duplicate ancestor path", scope: authorization.Scope{Type: "tenant", ID: "acme"}, ancestors: []authorization.Scope{{Type: "organization", ID: "north"}, {Type: "organization", ID: "north"}}},
		{name: "empty related resource", scope: authorization.Scope{Type: "tenant", ID: "acme"}, resources: []string{""}},
		{name: "oversized related resource", scope: authorization.Scope{Type: "tenant", ID: "acme"}, resources: []string{strings.Repeat("x", 513)}},
		{name: "padded related resource", scope: authorization.Scope{Type: "tenant", ID: "acme"}, resources: []string{" contest:1"}},
		{name: "control in related resource", scope: authorization.Scope{Type: "tenant", ID: "acme"}, resources: []string{"contest\n1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ScopeFilter(test.scope, test.ancestors, test.resources...)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("ScopeFilter() error = %v", err)
			}
		})
	}
}

func TestFilteredPolicyRejectsUnsafeAndExcessiveFilterValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter Filter
	}{
		{name: "too many values", filter: Filter{V0: make([]string, MaxFilterValues+1)}},
		{name: "oversized value", filter: Filter{V0: []string{strings.Repeat("x", MaxPolicyValueBytes+1)}}},
		{name: "control in value", filter: Filter{V0: []string{"role\nreader"}}},
		{name: "invalid wildcard prefix", filter: Filter{GroupSubjectPrefixes: []string{"service_%"}}},
		{name: "invalid scope", filter: Filter{Scope: &authorization.Scope{Type: "Tenant", ID: "acme"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			driver := &policyDriver{dialect: dialect.Postgres}
			adapter := newAdapterForTest(t, driver, settings.Casbin{})
			err := adapter.LoadFilteredPolicyContext(context.Background(), newAdapterTestModel(t), test.filter)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("LoadFilteredPolicyContext() error = %v", err)
			}
			if driver.queries != 0 {
				t.Fatalf("invalid filter reached the database %d times", driver.queries)
			}
		})
	}
}

func TestAdapterRejectsInvalidLoadInputsAndUsesDefaults(t *testing.T) {
	t.Parallel()

	driver := &policyDriver{dialect: dialect.Postgres}
	adapter, err := NewAdapter(driver, settings.Casbin{})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.table != DefaultTableName || adapter.limit != DefaultPolicyLimit {
		t.Fatalf("default adapter settings = (%q, %d)", adapter.table, adapter.limit)
	}
	if adapter.IsFiltered() {
		// The unfiltered adapter must remain available for a complete policy load.
		t.Fatal("new adapter unexpectedly reports a filtered load")
	}
	if err := (*Adapter)(nil).LoadPolicyContext(context.Background(), newAdapterTestModel(t)); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil adapter LoadPolicyContext() error = %v", err)
	}
	if err := adapter.LoadPolicyContext(nil, newAdapterTestModel(t)); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil context LoadPolicyContext() error = %v", err)
	}
	if err := adapter.LoadPolicyContext(context.Background(), nil); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil model LoadPolicyContext() error = %v", err)
	}
	if err := adapter.LoadFilteredPolicyContext(context.Background(), newAdapterTestModel(t), "not a filter"); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("invalid filter type error = %v", err)
	}
	if adapter.IsFiltered() {
		t.Fatal("adapter marked an invalid filter as loaded")
	}

	if _, err := NewAdapter(driver, settings.Casbin{PolicyLimit: 0}); err != nil {
		t.Fatalf("zero policy limit should use default: %v", err)
	}
	if _, err := NewAdapter(driver, settings.Casbin{TableName: "policy_2", PolicyLimit: 1}); err != nil {
		t.Fatalf("valid custom table and limit rejected: %v", err)
	}
}

func TestMixinDefinesStorageAndLookupIndexes(t *testing.T) {
	t.Parallel()

	fields := (Mixin{}).Fields()
	if len(fields) != 7 {
		t.Fatalf("policy storage fields = %d, want seven Casbin columns", len(fields))
	}
	for index, field := range fields {
		want := "ptype"
		if index > 0 {
			want = "v" + string(rune('0'+index-1))
		}
		if got := field.Descriptor().Name; got != want {
			t.Fatalf("field %d = %q, want %q", index, got, want)
		}
	}

	indexes := (Mixin{}).Indexes()
	if len(indexes) != 3 {
		t.Fatalf("policy indexes = %d, want unique policy key and two filter indexes", len(indexes))
	}
	if descriptor := indexes[0].Descriptor(); !descriptor.Unique || len(descriptor.Fields) != 7 {
		t.Fatalf("policy identity index = %#v", descriptor)
	}
}

var (
	errQueryFailure = errors.New("query failed")
	errScanFailure  = errors.New("scan failed")
	errRowsFailure  = errors.New("rows failed")
)

func newAdapterForTest(t *testing.T, driver *policyDriver, config settings.Casbin) *Adapter {
	t.Helper()
	adapter, err := NewAdapter(driver, config)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func newAdapterTestModel(t *testing.T) model.Model {
	t.Helper()
	parsed, err := model.NewModelFromString(adapterTestModel)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func containsString(values []any, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type policyDriver struct {
	dialect    string
	rows       [][]string
	query      string
	args       []any
	queryErr   error
	scanErr    error
	rowsErr    error
	queries    int
	rowsClosed bool
}

func (driver *policyDriver) Exec(context.Context, string, any, any) error { return nil }
func (driver *policyDriver) Tx(context.Context) (dialect.Tx, error)       { return nil, nil }
func (driver *policyDriver) Close() error                                 { return nil }
func (driver *policyDriver) Dialect() string                              { return driver.dialect }
func (driver *policyDriver) Query(_ context.Context, query string, args, output any) error {
	driver.queries++
	driver.query = query
	driver.args, _ = args.([]any)
	if driver.queryErr != nil {
		return driver.queryErr
	}
	rows, ok := output.(*entsql.Rows)
	if !ok {
		return errors.New("unexpected row destination")
	}
	*rows = entsql.Rows{ColumnScanner: &policyRows{driver: driver}}
	return nil
}

type policyRows struct {
	driver  *policyDriver
	current int
}

func (rows *policyRows) Close() error {
	rows.driver.rowsClosed = true
	return nil
}
func (*policyRows) ColumnTypes() ([]*stdsql.ColumnType, error) { return nil, nil }
func (*policyRows) Columns() ([]string, error)                 { return nil, nil }
func (rows *policyRows) Err() error                            { return rows.driver.rowsErr }
func (*policyRows) NextResultSet() bool                        { return false }
func (rows *policyRows) Next() bool {
	if rows.current == len(rows.driver.rows) {
		return false
	}
	rows.current++
	return true
}
func (rows *policyRows) Scan(destinations ...any) error {
	if rows.driver.scanErr != nil {
		return rows.driver.scanErr
	}
	values := rows.driver.rows[rows.current-1]
	for index, destination := range destinations {
		*destination.(*string) = values[index]
	}
	return nil
}
