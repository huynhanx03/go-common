package widecolumn

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/gocql/gocql"

	"github.com/huynhanx03/go-common/pkg/database"
	"github.com/huynhanx03/go-common/pkg/dto"
)

const (
	defaultMaxBatchSize = 50
	hardMaxBatchSize    = 100
	maxIdentifierBytes  = 48
	maxPagingStateBytes = 16 * 1024
	maxTTLSeconds       = 20 * 365 * 24 * 60 * 60
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Repository defines the interface for Wide Column DB repositories.
type Repository[T any] database.Repository[T, any]

// RepositoryOption customizes a BaseRepository without exposing its internals.
type RepositoryOption func(*RepositoryConfig) error

// RepositoryConfig is the option state used while constructing a repository.
// Its fields are intentionally private; use the With* options provided by this
// package so every value is validated consistently.
type RepositoryConfig struct {
	idColumn     string
	maxBatchSize int
	batchType    gocql.BatchType
	mapper       *Mapper
}

// WithIDColumn changes the single-column key used by Get, Exists, and Delete.
// Composite-partition-key access should live in a domain repository because it
// requires an explicit CQL data-access pattern.
func WithIDColumn(column string) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfiguration
		}
		config.idColumn = column
		return nil
	}
}

// WithMaxBatchSize sets the largest batch accepted by the bulk methods.
// Oversized batches fail before any query is submitted.
func WithMaxBatchSize(size int) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfiguration
		}
		if size <= 0 || size > hardMaxBatchSize {
			return fmt.Errorf(
				"%w: max batch size must be between 1 and %d",
				ErrInvalidConfiguration,
				hardMaxBatchSize,
			)
		}
		config.maxBatchSize = size
		return nil
	}
}

// WithBatchType selects logged or unlogged batches. Logged batches remain the
// default for compatibility with the original repository contract.
func WithBatchType(batchType gocql.BatchType) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfiguration
		}
		switch batchType {
		case gocql.LoggedBatch, gocql.UnloggedBatch:
			config.batchType = batchType
			return nil
		default:
			return fmt.Errorf("%w: unsupported batch type", ErrInvalidConfiguration)
		}
	}
}

// WithMapper injects a mapper for applications that need custom value
// conversions. A nil mapper is rejected rather than silently falling back to
// a global mutable dependency.
func WithMapper(mapper *Mapper) RepositoryOption {
	return func(config *RepositoryConfig) error {
		if config == nil {
			return ErrInvalidConfiguration
		}
		if mapper == nil {
			return fmt.Errorf("%w: nil mapper", ErrInvalidConfiguration)
		}
		config.mapper = mapper
		return nil
	}
}

// BaseRepository provides bounded, injection-safe common CQL operations.
// It deliberately supports only the data-access pattern that can be inferred
// from Model: a single key column and cursor-scanned table pages. Rich queries
// belong in domain repositories where partition and clustering keys are known.
type BaseRepository[T Model] struct {
	session         *gocql.Session
	tableName       string
	tableCQL        string
	idColumn        string
	columnNames     []string
	selectColumns   string
	insertStatement string
	insertTTL       string
	deleteStatement string
	getStatement    string
	findStatement   string
	existsStatement string
	maxBatchSize    int
	batchType       gocql.BatchType
	mapper          *Mapper
	initErr         error
}

// NewBaseRepository preserves the original one-result constructor. Invalid
// configuration is retained by the repository and every operation fails with
// that error; use NewCheckedBaseRepository to fail fast during boot.
func NewBaseRepository[T Model](
	session *gocql.Session,
	dummy T,
	options ...RepositoryOption,
) *BaseRepository[T] {
	repository, _ := newBaseRepository(session, dummy, options...)
	return repository
}

// NewCheckedBaseRepository validates the session, table, columns, key, and
// options immediately. It is the recommended constructor for new services.
func NewCheckedBaseRepository[T Model](
	session *gocql.Session,
	dummy T,
	options ...RepositoryOption,
) (*BaseRepository[T], error) {
	return newBaseRepository(session, dummy, options...)
}

func newBaseRepository[T Model](
	session *gocql.Session,
	dummy T,
	options ...RepositoryOption,
) (*BaseRepository[T], error) {
	config := RepositoryConfig{
		idColumn:     IDColumn,
		maxBatchSize: defaultMaxBatchSize,
		batchType:    gocql.LoggedBatch,
	}
	repository := &BaseRepository[T]{session: session}

	for _, option := range options {
		if option == nil {
			repository.initErr = fmt.Errorf("%w: nil repository option", ErrInvalidConfiguration)
			return repository, repository.initErr
		}
		if err := option(&config); err != nil {
			repository.initErr = err
			return repository, repository.initErr
		}
	}

	if session == nil {
		repository.initErr = ErrNilSession
		return repository, repository.initErr
	}

	definition, err := describeModel(dummy, config.idColumn)
	if err != nil {
		repository.initErr = err
		return repository, repository.initErr
	}

	repository.tableName = definition.tableName
	repository.tableCQL = definition.tableCQL
	repository.idColumn = definition.idColumn
	repository.columnNames = definition.columnNames
	repository.selectColumns = definition.selectColumns
	repository.maxBatchSize = config.maxBatchSize
	repository.batchType = config.batchType
	repository.mapper = config.mapper
	if repository.mapper == nil {
		repository.mapper = defaultMapper
	}
	repository.insertStatement = fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		definition.tableCQL,
		definition.selectColumns,
		placeholders(len(definition.columnNames)),
	)
	repository.insertTTL = repository.insertStatement + " USING TTL ?"
	repository.deleteStatement = fmt.Sprintf(
		"DELETE FROM %s WHERE %s = ?",
		definition.tableCQL,
		quoteIdentifier(definition.idColumn),
	)
	repository.getStatement = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = ? LIMIT 1",
		definition.selectColumns,
		definition.tableCQL,
		quoteIdentifier(definition.idColumn),
	)
	repository.findStatement = fmt.Sprintf(
		"SELECT %s FROM %s",
		definition.selectColumns,
		definition.tableCQL,
	)
	repository.existsStatement = fmt.Sprintf(
		"SELECT count(*) FROM %s WHERE %s = ?",
		definition.tableCQL,
		quoteIdentifier(definition.idColumn),
	)

	return repository, nil
}

// ValidationError reports a constructor error retained by the compatibility
// constructor. It returns nil only when the repository is ready for use.
func (r *BaseRepository[T]) ValidationError() error {
	if r == nil {
		return ErrUninitializedRepository
	}
	return r.initErr
}

// Create inserts a new model.
func (r *BaseRepository[T]) Create(ctx context.Context, model *T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	values, err := r.modelValues(model)
	if err != nil {
		return err
	}
	if err := r.session.Query(r.insertStatement, values...).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("widecolumn: create in %s: %w", r.tableName, err)
	}
	return nil
}

// CreateWithTTL inserts a new model with a positive, bounded TTL in seconds.
func (r *BaseRepository[T]) CreateWithTTL(ctx context.Context, model *T, ttl int) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if ttl <= 0 || ttl > maxTTLSeconds {
		return fmt.Errorf("%w: must be between 1 and %d seconds", ErrInvalidTTL, maxTTLSeconds)
	}
	values, err := r.modelValues(model)
	if err != nil {
		return err
	}
	arguments := make([]any, 0, len(values)+1)
	arguments = append(arguments, values...)
	arguments = append(arguments, ttl)
	if err := r.session.Query(r.insertTTL, arguments...).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("widecolumn: create with TTL in %s: %w", r.tableName, err)
	}
	return nil
}

// Update uses Cassandra's upsert semantics.
func (r *BaseRepository[T]) Update(ctx context.Context, model *T) error {
	return r.Create(ctx, model)
}

// Delete removes a model by its configured key column.
func (r *BaseRepository[T]) Delete(ctx context.Context, id any) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	if err := r.session.Query(r.deleteStatement, id).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("widecolumn: delete from %s: %w", r.tableName, err)
	}
	return nil
}

// Get retrieves a model by its configured key column.
func (r *BaseRepository[T]) Get(ctx context.Context, id any) (*T, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	if err := validateID(id); err != nil {
		return nil, err
	}

	iter := r.session.Query(r.getStatement, id).WithContext(ctx).Iter()
	row := make(map[string]any, len(r.columnNames))
	if !iter.MapScan(row) {
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("widecolumn: get from %s: %w", r.tableName, err)
		}
		return nil, ErrNotFound
	}

	var result T
	if err := r.mapper.Bind(row, &result); err != nil {
		return nil, joinIteratorError(
			fmt.Errorf("widecolumn: map row from %s: %w", r.tableName, err),
			iter.Close(),
		)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("widecolumn: close get iterator for %s: %w", r.tableName, err)
	}
	return &result, nil
}

// Find retrieves one bounded Cassandra page. Filters, sorts, and offset pages
// are rejected: applying them generically can cause ALLOW FILTERING/full-scan
// queries. Pagination.Cursor must be the opaque next_cursor returned by the
// previous response.
func (r *BaseRepository[T]) Find(
	ctx context.Context,
	opts *dto.QueryOptions,
) (*dto.Paginated[*T], error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	window, err := buildFindWindow(opts)
	if err != nil {
		return nil, err
	}

	query := r.session.Query(r.findStatement).
		WithContext(ctx).
		PageSize(window.pageSize).
		PageState(window.pageState)
	iter := query.Iter()
	records := make([]*T, 0, window.pageSize)
	for {
		row := make(map[string]any, len(r.columnNames))
		if !iter.MapScan(row) {
			break
		}

		var model T
		if err := r.mapper.Bind(row, &model); err != nil {
			return nil, joinIteratorError(
				fmt.Errorf("widecolumn: map row from %s: %w", r.tableName, err),
				iter.Close(),
			)
		}
		records = append(records, &model)
	}

	nextState := append([]byte(nil), iter.PageState()...)
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("widecolumn: close find iterator for %s: %w", r.tableName, err)
	}
	if len(nextState) > 0 && bytes.Equal(nextState, window.pageState) {
		return nil, fmt.Errorf("%w: database returned a non-advancing cursor", ErrInvalidCursor)
	}
	nextCursor, err := encodeCursor(nextState)
	if err != nil {
		return nil, err
	}
	var nextCursorValue any
	if nextCursor != "" {
		nextCursorValue = nextCursor
	}

	return &dto.Paginated[*T]{
		Records: &records,
		Pagination: &dto.PaginationMeta{
			CurrentPage: window.page,
			PageSize:    window.pageSize,
			HasNext:     nextCursor != "",
			HasPrev:     window.hasCursor,
			NextCursor:  nextCursorValue,
		},
	}, nil
}

// Exists checks if a model exists by its configured key column.
func (r *BaseRepository[T]) Exists(ctx context.Context, id any) (bool, error) {
	if err := r.ready(ctx); err != nil {
		return false, err
	}
	if err := validateID(id); err != nil {
		return false, err
	}
	var count int64
	if err := r.session.Query(r.existsStatement, id).WithContext(ctx).Scan(&count); err != nil {
		return false, fmt.Errorf("widecolumn: check existence in %s: %w", r.tableName, err)
	}
	return count > 0, nil
}

// CreateBulk inserts a bounded batch. Every model is validated before the
// batch is sent, preventing validation failures from causing partial writes.
func (r *BaseRepository[T]) CreateBulk(ctx context.Context, models []*T) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}
	if len(models) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(models), r.maxBatchSize)
	}

	values := make([][]any, len(models))
	for index, model := range models {
		modelValues, err := r.modelValues(model)
		if err != nil {
			return fmt.Errorf("widecolumn: validate bulk model %d: %w", index, err)
		}
		values[index] = modelValues
	}

	batch := r.session.NewBatch(r.batchType).WithContext(ctx)
	for _, modelValues := range values {
		batch.Query(r.insertStatement, modelValues...)
	}
	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("widecolumn: create batch in %s: %w", r.tableName, err)
	}
	return nil
}

// DeleteBulk removes a bounded batch of keys.
func (r *BaseRepository[T]) DeleteBulk(ctx context.Context, ids []any) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > r.maxBatchSize {
		return fmt.Errorf("%w: got %d, limit is %d", ErrBatchTooLarge, len(ids), r.maxBatchSize)
	}
	for index, id := range ids {
		if err := validateID(id); err != nil {
			return fmt.Errorf("widecolumn: validate bulk id %d: %w", index, err)
		}
	}

	batch := r.session.NewBatch(r.batchType).WithContext(ctx)
	for _, id := range ids {
		batch.Query(r.deleteStatement, id)
	}
	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("widecolumn: delete batch from %s: %w", r.tableName, err)
	}
	return nil
}

type modelDefinition struct {
	tableName     string
	tableCQL      string
	idColumn      string
	columnNames   []string
	selectColumns string
}

func describeModel[T Model](dummy T, idColumn string) (definition modelDefinition, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: model metadata panicked: %v", ErrInvalidModel, recovered)
		}
	}()
	if isNilValue(dummy) {
		return modelDefinition{}, fmt.Errorf("%w: nil model", ErrInvalidModel)
	}

	tableName, tableCQL, err := normalizeTableName(dummy.TableName())
	if err != nil {
		return modelDefinition{}, err
	}
	columns, selectColumns, err := normalizeColumns(dummy.ColumnNames())
	if err != nil {
		return modelDefinition{}, err
	}
	if values := dummy.ColumnValues(); len(values) != len(columns) {
		return modelDefinition{}, fmt.Errorf(
			"%w: model declares %d columns but %d values",
			ErrInvalidModel,
			len(columns),
			len(values),
		)
	}
	key, err := normalizeIdentifier(idColumn)
	if err != nil {
		return modelDefinition{}, fmt.Errorf("%w: invalid key column: %v", ErrInvalidIdentifier, err)
	}
	if !contains(columns, key) {
		return modelDefinition{}, fmt.Errorf(
			"%w: key column %q is not declared by the model",
			ErrInvalidModel,
			key,
		)
	}

	return modelDefinition{
		tableName:     tableName,
		tableCQL:      tableCQL,
		idColumn:      key,
		columnNames:   columns,
		selectColumns: selectColumns,
	}, nil
}

func normalizeTableName(value string) (string, string, error) {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return "", "", fmt.Errorf("%w: invalid table name", ErrInvalidIdentifier)
	}
	normalized := make([]string, len(parts))
	quoted := make([]string, len(parts))
	for index, part := range parts {
		identifier, err := normalizeIdentifier(part)
		if err != nil {
			return "", "", fmt.Errorf("%w: invalid table component %q", ErrInvalidIdentifier, part)
		}
		normalized[index] = identifier
		quoted[index] = quoteIdentifier(identifier)
	}
	return strings.Join(normalized, "."), strings.Join(quoted, "."), nil
}

func normalizeColumns(values []string) ([]string, string, error) {
	if len(values) == 0 {
		return nil, "", fmt.Errorf("%w: model declares no columns", ErrInvalidModel)
	}
	columns := make([]string, len(values))
	quoted := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		column, err := normalizeIdentifier(value)
		if err != nil {
			return nil, "", fmt.Errorf("%w: invalid column %q", ErrInvalidIdentifier, value)
		}
		if _, exists := seen[column]; exists {
			return nil, "", fmt.Errorf("%w: duplicate column %q", ErrInvalidModel, column)
		}
		seen[column] = struct{}{}
		columns[index] = column
		quoted[index] = quoteIdentifier(column)
	}
	return columns, strings.Join(quoted, ", "), nil
}

func normalizeIdentifier(value string) (string, error) {
	if value == "" || len(value) > maxIdentifierBytes || !identifierPattern.MatchString(value) {
		return "", ErrInvalidIdentifier
	}
	return strings.ToLower(value), nil
}

func quoteIdentifier(value string) string {
	return `"` + value + `"`
}

func placeholders(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = "?"
	}
	return strings.Join(values, ", ")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (r *BaseRepository[T]) ready(ctx context.Context) error {
	if r == nil {
		return ErrUninitializedRepository
	}
	if r.initErr != nil {
		return r.initErr
	}
	if r.session == nil {
		return ErrNilSession
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (r *BaseRepository[T]) modelValues(model *T) (values []any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: model metadata panicked: %v", ErrInvalidModel, recovered)
		}
	}()
	if model == nil || isNilValue(*model) {
		return nil, fmt.Errorf("%w: nil model", ErrInvalidModel)
	}
	value := *model
	columns, _, err := normalizeColumns(value.ColumnNames())
	if err != nil {
		return nil, err
	}
	if len(columns) != len(r.columnNames) {
		return nil, fmt.Errorf("%w: column set changed after repository construction", ErrInvalidModel)
	}
	for index := range columns {
		if columns[index] != r.columnNames[index] {
			return nil, fmt.Errorf("%w: column set changed after repository construction", ErrInvalidModel)
		}
	}
	values = value.ColumnValues()
	if len(values) != len(columns) {
		return nil, fmt.Errorf(
			"%w: model has %d columns but %d values",
			ErrInvalidModel,
			len(columns),
			len(values),
		)
	}
	return values, nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func validateID(id any) error {
	if isNilValue(id) {
		return ErrInvalidID
	}
	switch value := id.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return ErrInvalidID
		}
	case []byte:
		if len(value) == 0 {
			return ErrInvalidID
		}
	}
	return nil
}

type findWindow struct {
	page      int
	pageSize  int
	pageState []byte
	hasCursor bool
}

func buildFindWindow(opts *dto.QueryOptions) (findWindow, error) {
	if opts != nil && len(opts.Filters) > 0 {
		return findWindow{}, fmt.Errorf("%w: generic filters require an explicit CQL data-access pattern", ErrUnsupportedQuery)
	}
	if opts != nil && len(opts.Sort) > 0 {
		return findWindow{}, fmt.Errorf("%w: generic sorting requires clustering-key metadata", ErrUnsupportedQuery)
	}

	page := 1
	pageSize := dto.DefaultPageSize
	var cursor any
	if opts != nil && opts.Pagination != nil {
		pagination := opts.Pagination
		if pagination.Page < 0 || pagination.PageSize < 0 {
			return findWindow{}, dto.ErrInvalidPagination
		}
		if pagination.Page > 0 {
			page = pagination.Page
		}
		if pagination.PageSize > 0 {
			pageSize = pagination.PageSize
		}
		cursor = pagination.Cursor
	}
	if pageSize > dto.MaxPageSize {
		return findWindow{}, dto.ErrInvalidPagination
	}

	pageState, hasCursor, err := decodeCursor(cursor)
	if err != nil {
		return findWindow{}, err
	}
	if page > 1 && !hasCursor {
		return findWindow{}, fmt.Errorf("%w: offset pages are not supported", ErrUnsupportedQuery)
	}

	return findWindow{
		page:      page,
		pageSize:  pageSize,
		pageState: pageState,
		hasCursor: hasCursor,
	}, nil
}

func decodeCursor(cursor any) ([]byte, bool, error) {
	switch value := cursor.(type) {
	case nil:
		return nil, false, nil
	case string:
		if value == "" {
			return nil, false, nil
		}
		maxEncodedBytes := base64.RawURLEncoding.EncodedLen(maxPagingStateBytes)
		if len(value) > maxEncodedBytes {
			return nil, false, ErrInvalidCursor
		}
		state, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(state) == 0 || len(state) > maxPagingStateBytes {
			return nil, false, ErrInvalidCursor
		}
		if base64.RawURLEncoding.EncodeToString(state) != value {
			return nil, false, ErrInvalidCursor
		}
		return state, true, nil
	case []byte:
		if len(value) == 0 {
			return nil, false, nil
		}
		if len(value) > maxPagingStateBytes {
			return nil, false, ErrInvalidCursor
		}
		return append([]byte(nil), value...), true, nil
	default:
		return nil, false, ErrInvalidCursor
	}
}

func encodeCursor(state []byte) (string, error) {
	if len(state) == 0 {
		return "", nil
	}
	if len(state) > maxPagingStateBytes {
		return "", ErrInvalidCursor
	}
	return base64.RawURLEncoding.EncodeToString(state), nil
}

func joinIteratorError(operationErr, closeErr error) error {
	if closeErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, fmt.Errorf("widecolumn: close iterator: %w", closeErr))
}
