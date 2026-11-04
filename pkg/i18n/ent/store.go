package ent

import (
	"context"
	"database/sql"
	"reflect"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/i18n"
)

type Store struct{ driver dialect.Driver }

// NewStore borrows a PostgreSQL Ent driver. The service owns its connection,
// schema migrations, transaction lifecycle, and application shutdown.
func NewStore(driver dialect.Driver) (*Store, error) {
	if isNil(driver) || driver.Dialect() != dialect.Postgres {
		return nil, i18n.ErrInvalidConfig
	}
	return &Store{driver: driver}, nil
}

func (s *Store) Read(ctx context.Context, domain string) (i18n.State, error) {
	if err := s.validate(ctx, domain); err != nil {
		return i18n.State{}, err
	}
	query := `SELECT revision,
		CASE WHEN octet_length(state::text) <= $2 THEN state::text ELSE NULL END,
		octet_length(state::text)
		FROM ` + TableName + ` WHERE domain=$1`
	var rows entsql.Rows
	if err := s.driver.Query(ctx, query, []any{domain, MaxJSONBTextBytes}, &rows); err != nil {
		return i18n.State{}, storageFailure(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return i18n.State{}, storageFailure(err)
		}
		return i18n.State{}, i18n.ErrNotFound
	}
	var revision int64
	var encoded sql.NullString
	var size sql.NullInt64
	if err := rows.Scan(&revision, &encoded, &size); err != nil {
		return i18n.State{}, storageFailure(err)
	}
	if rows.Next() {
		return i18n.State{}, sourceFailure(i18n.ErrInvalidDefinition)
	}
	if err := rows.Err(); err != nil {
		return i18n.State{}, storageFailure(err)
	}
	if err := rows.Close(); err != nil {
		return i18n.State{}, storageFailure(err)
	}
	if !size.Valid || size.Int64 > MaxJSONBTextBytes {
		return i18n.State{}, sourceFailure(i18n.ErrLimit)
	}
	if !encoded.Valid || revision <= 0 {
		return i18n.State{}, sourceFailure(i18n.ErrInvalidDefinition)
	}
	var state i18n.State
	if err := jsoncodec.Unmarshal([]byte(encoded.String), &state); err != nil {
		return i18n.State{}, sourceFailure(i18n.ErrInvalidDefinition)
	}
	if state.Draft.Revision != revision {
		return i18n.State{}, sourceFailure(i18n.ErrInvalidDefinition)
	}
	validated, err := i18n.ValidateState(domain, state)
	if err != nil {
		return i18n.State{}, sourceFailure(err)
	}
	return validated, nil
}

// CompareAndSwap stores the whole catalog in one row, atomically advancing its
// revision. A zero expected revision creates the domain if it does not exist.
func (s *Store) CompareAndSwap(ctx context.Context, domain string, expected int64, next i18n.State) error {
	if err := s.validate(ctx, domain); err != nil {
		return err
	}
	if expected < 0 || expected == int64(^uint64(0)>>1) || next.Draft.Revision != expected+1 {
		return i18n.ErrConflict
	}
	state, err := i18n.ValidateState(domain, next)
	if err != nil {
		return err
	}
	encoded, err := jsoncodec.Marshal(state)
	if err != nil {
		return sourceFailure(i18n.ErrInvalidDefinition)
	}
	var result sql.Result
	if expected == 0 {
		result, err = execResult(ctx, s.driver, `INSERT INTO `+TableName+` (domain,revision,state,created_at,updated_at)
			VALUES ($1,$2,$3::jsonb,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT (domain) DO NOTHING`,
			domain, state.Draft.Revision, string(encoded))
	} else {
		result, err = execResult(ctx, s.driver, `UPDATE `+TableName+`
			SET revision=$3,state=$4::jsonb,updated_at=CURRENT_TIMESTAMP
			WHERE domain=$1 AND revision=$2`, domain, expected, state.Draft.Revision, string(encoded))
	}
	if err != nil {
		return storageFailure(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return storageFailure(err)
	}
	if changed != 1 {
		return i18n.ErrConflict
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, domain string, expected int64) error {
	if err := s.validate(ctx, domain); err != nil {
		return err
	}
	if expected <= 0 || expected == int64(^uint64(0)>>1) {
		return i18n.ErrConflict
	}
	result, err := execResult(ctx, s.driver, `DELETE FROM `+TableName+` WHERE domain=$1 AND revision=$2`, domain, expected)
	if err != nil {
		return storageFailure(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return storageFailure(err)
	}
	if changed != 1 {
		return i18n.ErrConflict
	}
	return nil
}

func (s *Store) Domains(ctx context.Context) ([]string, error) {
	if s == nil || isNil(s.driver) {
		return nil, i18n.ErrInvalidConfig
	}
	if ctx == nil {
		return nil, i18n.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows entsql.Rows
	if err := s.driver.Query(ctx, `SELECT domain FROM `+TableName+` ORDER BY domain LIMIT $1`, []any{i18n.MaxDomains + 1}, &rows); err != nil {
		return nil, storageFailure(err)
	}
	defer rows.Close()
	domains := make([]string, 0)
	for rows.Next() {
		if len(domains) == i18n.MaxDomains {
			return nil, sourceFailure(i18n.ErrLimit)
		}
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, storageFailure(err)
		}
		if i18n.ValidateDomain(domain) != nil {
			return nil, sourceFailure(i18n.ErrInvalidDefinition)
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		return nil, storageFailure(err)
	}
	if err := rows.Close(); err != nil {
		return nil, storageFailure(err)
	}
	return domains, nil
}

func (s *Store) validate(ctx context.Context, domain string) error {
	if s == nil || isNil(s.driver) {
		return i18n.ErrInvalidConfig
	}
	if ctx == nil {
		return i18n.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return i18n.ValidateDomain(domain)
}

type redactedFailure struct{ cause error }

func (e redactedFailure) Error() string   { return i18n.ErrSource.Error() }
func (e redactedFailure) Unwrap() []error { return []error{i18n.ErrSource, e.cause} }

func storageFailure(err error) error {
	if err == nil {
		return nil
	}
	return redactedFailure{cause: err}
}

func sourceFailure(err error) error {
	return redactedFailure{cause: err}
}

func execResult(ctx context.Context, executor dialect.ExecQuerier, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	if err := executor.Exec(ctx, query, args, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, sql.ErrNoRows
	}
	return result, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return ref.IsNil()
	}
	return false
}

var _ i18n.Store = (*Store)(nil)
