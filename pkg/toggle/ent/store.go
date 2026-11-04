package ent

import (
	"context"
	"database/sql"
	"errors"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

const definitionSchemaVersion = 1

// Store persists toggle definitions in the canonical PostgreSQL tables. It
// borrows the caller's driver and never closes it or runs migrations.
type Store struct{ driver dialect.Driver }

// NewStore creates a PostgreSQL Store over a caller-owned Ent SQL driver.
func NewStore(driver dialect.Driver) (*Store, error) {
	if core.IsNil(driver) || driver.Dialect() != dialect.Postgres {
		return nil, toggle.ErrInvalidConfig
	}
	return &Store{driver: driver}, nil
}

type definitionEnvelope struct {
	SchemaVersion int           `json:"schema_version"`
	Rules         []toggle.Rule `json:"rules"`
}

// Load returns one complete, revision-consistent scope snapshot. The single
// joined statement observes one PostgreSQL statement snapshot and caps both
// row count and serialized definition bytes before data is decoded in Go.
func (s *Store) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	if s == nil || core.IsNil(s.driver) {
		return toggle.Snapshot{}, toggle.ErrInvalidConfig
	}
	if err := storageContext(ctx); err != nil {
		return toggle.Snapshot{}, err
	}
	if err := toggle.ValidateScope(scope); err != nil {
		return toggle.Snapshot{}, err
	}
	query := `
		SELECT s.revision, f.key, f.description, f.enabled,
		   CASE WHEN octet_length(f.definition::text) <= $3 THEN f.definition::text ELSE NULL END,
		       octet_length(f.definition::text)
		FROM toggle_scopes AS s
		LEFT JOIN toggle_flags AS f
		  ON f.namespace = s.namespace AND f.environment = s.environment
		WHERE s.namespace = $1 AND s.environment = $2
		ORDER BY f.key
		LIMIT $4`
	var rows entsql.Rows
	if err := s.driver.Query(ctx, query, []any{scope.Namespace, scope.Environment, core.MaxDefinitionSize, core.MaxFlagsPerSnapshot + 1}, &rows); err != nil {
		return toggle.Snapshot{}, storageFailure(err)
	}
	defer rows.Close()

	snapshot := toggle.Snapshot{Scope: scope}
	var totalBytes int64
	var revisionSet bool
	for rows.Next() {
		var revision int64
		var key, description, definition sql.NullString
		var enabled sql.NullBool
		var definitionBytes sql.NullInt64
		if err := rows.Scan(&revision, &key, &description, &enabled, &definition, &definitionBytes); err != nil {
			return toggle.Snapshot{}, storageFailure(err)
		}
		if revisionSet && snapshot.Revision != revision {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrInvalidDefinition)
		}
		snapshot.Revision = revision
		revisionSet = true
		if !key.Valid {
			// LEFT JOIN emits a null key only for an existing scope with no flags.
			continue
		}
		if len(snapshot.Flags) == core.MaxFlagsPerSnapshot {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrLimit)
		}
		if !description.Valid || !enabled.Valid || !definitionBytes.Valid {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrInvalidDefinition)
		}
		if definitionBytes.Int64 > core.MaxDefinitionSize {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrLimit)
		}
		if !definition.Valid {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrInvalidDefinition)
		}
		totalBytes += int64(len(key.String)+len(description.String)) + definitionBytes.Int64
		if totalBytes > core.MaxSnapshotSize {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrLimit)
		}
		var envelope definitionEnvelope
		if err := jsoncodec.Unmarshal([]byte(definition.String), &envelope); err != nil || envelope.SchemaVersion != definitionSchemaVersion {
			return toggle.Snapshot{}, core.SourceError(toggle.ErrInvalidDefinition)
		}
		snapshot.Flags = append(snapshot.Flags, toggle.Flag{
			Key: key.String, Description: description.String, Enabled: enabled.Bool, Rules: envelope.Rules,
		})
	}
	if err := rows.Err(); err != nil {
		return toggle.Snapshot{}, storageFailure(err)
	}
	if err := storageContext(ctx); err != nil {
		return toggle.Snapshot{}, err
	}
	if err := rows.Close(); err != nil {
		return toggle.Snapshot{}, storageFailure(err)
	}
	normalized, err := toggle.NormalizeSnapshot(scope, snapshot)
	if err != nil {
		return toggle.Snapshot{}, core.SourceError(err)
	}
	return normalized, nil
}

// Put atomically claims the next scope revision and upserts a complete flag.
func (s *Store) Put(ctx context.Context, scope toggle.Scope, flag toggle.Flag, expectedRevision int64) (int64, error) {
	if err := s.validateWrite(ctx, scope, expectedRevision); err != nil {
		return 0, err
	}
	normalized, err := toggle.NormalizeFlag(flag)
	if err != nil {
		return 0, err
	}
	definition, err := jsoncodec.Marshal(definitionEnvelope{SchemaVersion: definitionSchemaVersion, Rules: normalized.Rules})
	if err != nil {
		return 0, core.SourceError(toggle.ErrInvalidDefinition)
	}
	if len(definition) > core.MaxDefinitionSize {
		return 0, core.SourceError(toggle.ErrLimit)
	}

	return s.withRevision(ctx, scope, expectedRevision, func(tx dialect.Tx) error {
		count, exists, err := flagCount(ctx, tx, scope, normalized.Key)
		if err != nil {
			return storageFailure(err)
		}
		if !exists && count >= core.MaxFlagsPerSnapshot {
			return core.SourceError(toggle.ErrLimit)
		}
		if err := execute(ctx, tx, `
		INSERT INTO toggle_flags(namespace, environment, key, enabled, description, definition, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (namespace, environment, key) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    description = EXCLUDED.description,
		    definition = EXCLUDED.definition,
		    updated_at = CURRENT_TIMESTAMP`,
			scope.Namespace, scope.Environment, normalized.Key, normalized.Enabled, normalized.Description, string(definition)); err != nil {
			return storageFailure(err)
		}
		return nil
	})
}

// Delete atomically claims the next scope revision and removes an existing flag.
func (s *Store) Delete(ctx context.Context, scope toggle.Scope, key string, expectedRevision int64) (int64, error) {
	if err := s.validateWrite(ctx, scope, expectedRevision); err != nil {
		return 0, err
	}
	if err := validateKey(key); err != nil {
		return 0, err
	}
	return s.withRevision(ctx, scope, expectedRevision, func(tx dialect.Tx) error {
		result, err := executeResult(ctx, tx, `DELETE FROM toggle_flags WHERE namespace=$1 AND environment=$2 AND key=$3`, scope.Namespace, scope.Environment, key)
		if err != nil {
			return storageFailure(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return storageFailure(err)
		}
		if changed == 0 {
			return storeFailure(toggle.ErrNotFound, nil)
		}
		return nil
	})
}

func (s *Store) withRevision(ctx context.Context, scope toggle.Scope, expectedRevision int64, write func(dialect.Tx) error) (revision int64, err error) {
	tx, err := s.driver.Tx(ctx)
	if err != nil {
		return 0, storageFailure(err)
	}
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				err = errors.Join(err, storageFailure(rollbackErr))
			}
		}
	}()

	if err = ensureScope(ctx, tx, scope); err != nil {
		return 0, err
	}
	revision, err = claimRevision(ctx, tx, scope, expectedRevision)
	if err != nil {
		return 0, err
	}
	if err = write(tx); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, storageFailure(err)
	}
	committed = true
	return revision, nil
}

func (s *Store) validateWrite(ctx context.Context, scope toggle.Scope, expectedRevision int64) error {
	if s == nil || core.IsNil(s.driver) {
		return toggle.ErrInvalidConfig
	}
	if err := storageContext(ctx); err != nil {
		return err
	}
	if err := toggle.ValidateScope(scope); err != nil {
		return err
	}
	return core.ValidateExpectedRevision(expectedRevision)
}

func validateKey(key string) error {
	return core.Identifier(key, core.MaxIdentifierLength, toggle.ErrInvalidDefinition)
}

func storageContext(ctx context.Context) error {
	return core.ValidateContext(ctx)
}

func ensureScope(ctx context.Context, tx dialect.Tx, scope toggle.Scope) error {
	return execute(ctx, tx, `
		INSERT INTO toggle_scopes(namespace, environment, revision)
		VALUES ($1, $2, 0)
		ON CONFLICT (namespace, environment) DO NOTHING`, scope.Namespace, scope.Environment)
}

func claimRevision(ctx context.Context, tx dialect.Tx, scope toggle.Scope, expected int64) (int64, error) {
	result, err := executeResult(ctx, tx, `
		UPDATE toggle_scopes SET revision=$1
		WHERE namespace=$2 AND environment=$3 AND revision=$4`,
		expected+1, scope.Namespace, scope.Environment, expected)
	if err != nil {
		return 0, storageFailure(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, storageFailure(err)
	}
	if changed != 1 {
		return 0, storeFailure(toggle.ErrConflict, nil)
	}
	return expected + 1, nil
}

func flagCount(ctx context.Context, tx dialect.Tx, scope toggle.Scope, key string) (int64, bool, error) {
	var rows entsql.Rows
	if err := tx.Query(ctx, `
		SELECT COUNT(*), COALESCE(bool_or(key = $3), FALSE)
		FROM toggle_flags WHERE namespace=$1 AND environment=$2`,
		[]any{scope.Namespace, scope.Environment, key}, &rows); err != nil {
		return 0, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, false, err
		}
		return 0, false, errors.New("count query returned no row")
	}
	var count int64
	var exists bool
	if err := rows.Scan(&count, &exists); err != nil {
		return 0, false, err
	}
	return count, exists, rows.Err()
}

func execute(ctx context.Context, executor dialect.ExecQuerier, query string, args ...any) error {
	_, err := executeResult(ctx, executor, query, args...)
	return err
}

func executeResult(ctx context.Context, executor dialect.ExecQuerier, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	if err := executor.Exec(ctx, query, args, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("driver returned no SQL result")
	}
	return result, nil
}

type safeStoreError struct {
	cause error
}

func (e safeStoreError) Error() string { return "storage operation failed" }
func (e safeStoreError) Unwrap() error { return e.cause }

func storeFailure(category, cause error) error {
	return core.Wrap(category, cause)
}
func storageFailure(cause error) error { return safeStoreError{cause: cause} }

var _ toggle.Store = (*Store)(nil)
