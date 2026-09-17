package widecolumn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/huynhanx03/go-common/pkg/dto"
)

type repositoryTestModel struct {
	ID    string `cql:"id"`
	Value string `cql:"value"`
}

type modelWithCustomKey struct {
	External string `json:"external_key"`
	Value    string `json:"value"`
}

func (model modelWithCustomKey) TableName() string {
	return "custom_key_table"
}

func (model modelWithCustomKey) ColumnNames() []string {
	return []string{"external_key", "value"}
}

func (model modelWithCustomKey) ColumnValues() []any {
	return []any{model.External, model.Value}
}

func (model repositoryTestModel) TableName() string {
	return "Test_Keyspace.Test_Table"
}

func (model repositoryTestModel) ColumnNames() []string {
	return []string{"ID", "Value"}
}

func (model repositoryTestModel) ColumnValues() []any {
	return []any{model.ID, model.Value}
}

type invalidTableModel repositoryTestModel

func (model invalidTableModel) TableName() string {
	return "safe_table; DROP TABLE users"
}

func (model invalidTableModel) ColumnNames() []string {
	return []string{"id", "value"}
}

func (model invalidTableModel) ColumnValues() []any {
	return []any{model.ID, model.Value}
}

type invalidColumnModel repositoryTestModel

func (model invalidColumnModel) TableName() string {
	return "safe_table"
}

func (model invalidColumnModel) ColumnNames() []string {
	return []string{"id", "value) VALUES (?); TRUNCATE users; --"}
}

func (model invalidColumnModel) ColumnValues() []any {
	return []any{model.ID, model.Value}
}

type missingKeyModel repositoryTestModel

func (model missingKeyModel) TableName() string {
	return "safe_table"
}

func (model missingKeyModel) ColumnNames() []string {
	return []string{"external_key", "value"}
}

func (model missingKeyModel) ColumnValues() []any {
	return []any{model.ID, model.Value}
}

func TestNewCheckedBaseRepositoryBuildsValidatedStatements(t *testing.T) {
	repository, err := NewCheckedBaseRepository(
		&gocql.Session{},
		repositoryTestModel{},
		WithMaxBatchSize(25),
		WithBatchType(gocql.UnloggedBatch),
	)

	require.NoError(t, err)
	assert.Equal(t, `"test_keyspace"."test_table"`, repository.tableCQL)
	assert.Equal(t, `"id", "value"`, repository.selectColumns)
	assert.Equal(t, `INSERT INTO "test_keyspace"."test_table" ("id", "value") VALUES (?, ?)`, repository.insertStatement)
	assert.Equal(t, `SELECT "id", "value" FROM "test_keyspace"."test_table"`, repository.findStatement)
	assert.NotContains(t, repository.findStatement, "*")
	assert.Equal(t, 25, repository.maxBatchSize)
	assert.Equal(t, gocql.UnloggedBatch, repository.batchType)
}

func TestRepositoryAcceptsAnExplicitMapper(t *testing.T) {
	mapper := NewMapper()
	repository, err := NewCheckedBaseRepository(
		&gocql.Session{},
		repositoryTestModel{},
		WithMapper(mapper),
	)

	require.NoError(t, err)
	assert.Same(t, mapper, repository.mapper)
}

func TestRepositorySupportsAnExplicitSingleKeyColumn(t *testing.T) {
	// This model documents the option without making the generic repository
	// depend on a domain key convention.
	repository, err := NewCheckedBaseRepository(
		&gocql.Session{},
		modelWithCustomKey{External: "", Value: ""},
		WithIDColumn("external_key"),
	)

	require.NoError(t, err)
	assert.Contains(t, repository.getStatement, `WHERE "external_key" = ?`)
}

func TestNewCheckedBaseRepositoryRejectsUnsafeMetadata(t *testing.T) {
	tests := []struct {
		name  string
		build func() error
		want  error
	}{
		{
			name: "nil session",
			build: func() error {
				_, err := NewCheckedBaseRepository(nil, repositoryTestModel{})
				return err
			},
			want: ErrNilSession,
		},
		{
			name: "unsafe table",
			build: func() error {
				_, err := NewCheckedBaseRepository(&gocql.Session{}, invalidTableModel{})
				return err
			},
			want: ErrInvalidIdentifier,
		},
		{
			name: "unsafe column",
			build: func() error {
				_, err := NewCheckedBaseRepository(&gocql.Session{}, invalidColumnModel{})
				return err
			},
			want: ErrInvalidIdentifier,
		},
		{
			name: "missing default key",
			build: func() error {
				_, err := NewCheckedBaseRepository(&gocql.Session{}, missingKeyModel{})
				return err
			},
			want: ErrInvalidModel,
		},
		{
			name: "invalid option",
			build: func() error {
				_, err := NewCheckedBaseRepository(
					&gocql.Session{},
					repositoryTestModel{},
					WithMaxBatchSize(0),
				)
				return err
			},
			want: ErrInvalidConfiguration,
		},
		{
			name: "unsafe batch limit",
			build: func() error {
				_, err := NewCheckedBaseRepository(
					&gocql.Session{},
					repositoryTestModel{},
					WithMaxBatchSize(hardMaxBatchSize+1),
				)
				return err
			},
			want: ErrInvalidConfiguration,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.ErrorIs(t, test.build(), test.want)
		})
	}
}

func TestNewBaseRepositoryRetainsValidationError(t *testing.T) {
	repository := NewBaseRepository(nil, repositoryTestModel{})

	assert.ErrorIs(t, repository.ValidationError(), ErrNilSession)
	assert.ErrorIs(t, repository.Create(context.Background(), &repositoryTestModel{}), ErrNilSession)
}

func TestRepositoryRejectsInvalidInputBeforeQuery(t *testing.T) {
	repository, err := NewCheckedBaseRepository(&gocql.Session{}, repositoryTestModel{})
	require.NoError(t, err)

	assert.ErrorIs(t, repository.Create(nil, &repositoryTestModel{}), ErrInvalidContext)
	assert.ErrorIs(t, repository.Create(context.Background(), nil), ErrInvalidModel)
	assert.ErrorIs(t, repository.Delete(context.Background(), nil), ErrInvalidID)
	assert.ErrorIs(t, repository.Delete(context.Background(), "  "), ErrInvalidID)
	assert.ErrorIs(
		t,
		repository.CreateWithTTL(context.Background(), &repositoryTestModel{}, 0),
		ErrInvalidTTL,
	)
}

func TestBuildFindWindowDefaultsWithoutMutatingInput(t *testing.T) {
	options := &dto.QueryOptions{Pagination: &dto.PaginationOptions{}}

	window, err := buildFindWindow(options)

	require.NoError(t, err)
	assert.Equal(t, 1, window.page)
	assert.Equal(t, dto.DefaultPageSize, window.pageSize)
	assert.False(t, window.hasCursor)
	assert.Zero(t, options.Pagination.Page)
	assert.Zero(t, options.Pagination.PageSize)
}

func TestBuildFindWindowRejectsUnsafeGenericQueries(t *testing.T) {
	tests := []struct {
		name    string
		options *dto.QueryOptions
		want    error
	}{
		{
			name: "filter",
			options: &dto.QueryOptions{
				Filters: []dto.SearchFilter{{Key: "status", Value: "active"}},
			},
			want: ErrUnsupportedQuery,
		},
		{
			name: "sort",
			options: &dto.QueryOptions{
				Sort: []dto.SortOption{{Key: "created_at", Order: -1}},
			},
			want: ErrUnsupportedQuery,
		},
		{
			name: "offset page",
			options: &dto.QueryOptions{
				Pagination: &dto.PaginationOptions{Page: 2, PageSize: 10},
			},
			want: ErrUnsupportedQuery,
		},
		{
			name: "oversized page",
			options: &dto.QueryOptions{
				Pagination: &dto.PaginationOptions{PageSize: dto.MaxPageSize + 1},
			},
			want: dto.ErrInvalidPagination,
		},
		{
			name: "invalid cursor type",
			options: &dto.QueryOptions{
				Pagination: &dto.PaginationOptions{Cursor: 42},
			},
			want: ErrInvalidCursor,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildFindWindow(test.options)
			assert.ErrorIs(t, err, test.want)
		})
	}
}

func TestCursorRoundTripIsOpaqueBoundedAndCopied(t *testing.T) {
	state := []byte{0x00, 0x7f, 0xff, 0x42}
	cursor, err := encodeCursor(state)
	require.NoError(t, err)
	assert.NotContains(t, cursor, "=")

	decoded, present, err := decodeCursor(cursor)
	require.NoError(t, err)
	assert.True(t, present)
	assert.Equal(t, state, decoded)

	raw := []byte("driver-owned-state")
	decoded, present, err = decodeCursor(raw)
	require.NoError(t, err)
	assert.True(t, present)
	raw[0] = 'X'
	assert.Equal(t, byte('d'), decoded[0])

	_, _, err = decodeCursor(strings.Repeat("a", maxPagingStateBytes*2))
	assert.ErrorIs(t, err, ErrInvalidCursor)
	_, err = encodeCursor(make([]byte, maxPagingStateBytes+1))
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestRepositorySourceKeepsCursorScansBoundedAndClosesIterators(t *testing.T) {
	source, err := os.ReadFile("repository.go")
	require.NoError(t, err)
	contents := string(source)

	assert.NotContains(t, contents, "SELECT *")
	assert.NotContains(t, contents, "TODO")
	assert.Contains(t, contents, "PageSize(window.pageSize)")
	assert.Contains(t, contents, "PageState(window.pageState)")
	assert.GreaterOrEqual(t, strings.Count(contents, "iter.Close()"), 5)
	assert.NotContains(t, contents, "r.mapper.Bind(row, &model); err != nil {\n\t\t\tcontinue")
}

func TestValidationErrorsRemainInspectable(t *testing.T) {
	_, _, err := normalizeTableName("keyspace.table.extra")
	assert.True(t, errors.Is(err, ErrInvalidIdentifier))
}
