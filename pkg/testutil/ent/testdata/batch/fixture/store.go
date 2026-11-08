package fixture

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	"github.com/huynhanx03/go-common/pkg/coordination"
	gen "github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent"
	"github.com/huynhanx03/go-common/pkg/testutil/ent/testdata/batch/fixture/ent/checkpoint"
	"github.com/huynhanx03/go-common/pkg/workflow/batch"
)

var ErrCheckpointNotFound = errors.New("batch checkpoint not found")

// Store is a PostgreSQL-backed reference implementation for application-owned
// schemas and business transactions. The generated Ent client remains owned by
// the application that constructs this Store.
type Store struct {
	client    *gen.Client
	authority string
}

func NewStore(client *gen.Client, authority string) *Store {
	return &Store{client: client, authority: authority}
}

func Migrate(ctx context.Context, client *gen.Client) error {
	return client.Schema.Create(ctx, schema.WithGlobalUniqueID(false))
}

func (s *Store) Open(ctx context.Context, definition batch.Definition) (batch.Checkpoint, error) {
	if ctx == nil || definition.Key == "" || definition.InputFingerprint == "" || definition.CursorSchema == "" || s.authority == "" {
		return batch.Checkpoint{}, batch.ErrInvalidConfig
	}
	if err := s.client.Checkpoint.Create().
		SetJobKey(definition.Key).
		SetInputFingerprint(definition.InputFingerprint).
		SetCursorSchema(definition.CursorSchema).
		SetBoundary(cloneBytes(definition.Boundary)).
		SetCursor([]byte{}).
		SetStatus(string(batch.Ready)).
		SetVersion(0).
		SetLastStepID("").
		SetProcessedCount(0).
		SetOwner("").
		SetFenceAuthority(s.authority).
		SetFence(0).
		SetLeaseUntil(time.Time{}).
		SetCreatedAt(time.Now().UTC()).
		SetUpdatedAt(time.Now().UTC()).
		OnConflictColumns("job_key").Ignore().Exec(ctx); err != nil {
		return batch.Checkpoint{}, err
	}
	checkpoint, err := s.Get(ctx, definition.Key)
	if err != nil {
		return batch.Checkpoint{}, err
	}
	if !sameDefinition(checkpoint.Definition, definition) {
		return batch.Checkpoint{}, batch.ErrDefinitionConflict
	}
	if checkpoint.Fence.Authority != s.authority {
		return batch.Checkpoint{}, coordination.ErrAuthorityMismatch
	}
	return checkpoint, nil
}

func (s *Store) Get(ctx context.Context, key string) (batch.Checkpoint, error) {
	if ctx == nil || key == "" {
		return batch.Checkpoint{}, batch.ErrInvalidConfig
	}
	entity, err := s.client.Checkpoint.Query().Where(checkpoint.JobKeyEQ(key)).Only(ctx)
	if err != nil {
		if gen.IsNotFound(err) {
			return batch.Checkpoint{}, ErrCheckpointNotFound
		}
		return batch.Checkpoint{}, err
	}
	return fromEntity(entity), nil
}

func (s *Store) TryAcquireFenced(ctx context.Context, request coordination.AcquireRequest) (coordination.FencedLease, error) {
	if ctx == nil || request.Resource == "" || request.Owner == "" || request.TTL <= 0 || s.authority == "" {
		return nil, coordination.ErrInvalidConfig
	}
	var grant coordination.Grant
	err := s.withTx(ctx, func(tx *gen.Tx) error {
		entity, err := tx.Checkpoint.Query().Where(checkpoint.JobKeyEQ(request.Resource)).ForUpdate().Only(ctx)
		if err != nil {
			if gen.IsNotFound(err) {
				return ErrCheckpointNotFound
			}
			return err
		}
		if entity.FenceAuthority != s.authority {
			return coordination.ErrAuthorityMismatch
		}
		now, err := databaseNow(ctx, tx)
		if err != nil {
			return err
		}
		if entity.Status == string(batch.Completed) {
			return batch.ErrCompleted
		}
		if entity.Owner != "" && entity.LeaseUntil.After(now) {
			return coordination.ErrHeld
		}
		if entity.Fence == math.MaxInt64 {
			return coordination.ErrInvalidFence
		}
		grant = coordination.Grant{
			Resource: request.Resource,
			Owner:    request.Owner,
			Fence:    coordination.FenceToken{Authority: s.authority, Value: entity.Fence + 1},
		}
		_, err = tx.Checkpoint.UpdateOneID(entity.ID).
			SetStatus(string(batch.Running)).
			SetOwner(request.Owner).
			SetFence(grant.Fence.Value).
			SetLeaseUntil(now.Add(request.TTL)).
			SetUpdatedAt(now).
			Save(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &lease{store: s, grant: grant, ttl: request.TTL}, nil
}

func (s *Store) Apply(ctx context.Context, request batch.StepRequest, step batch.Step) (batch.Checkpoint, error) {
	if ctx == nil || step == nil || request.Grant.Resource == "" || request.Grant.Owner == "" || request.Grant.Fence.Authority != s.authority || request.Grant.Fence.Value <= 0 || request.StepID == "" || request.ExpectedVersion < 0 || request.ExpectedVersion == math.MaxInt64 {
		return batch.Checkpoint{}, coordination.ErrInvalidFence
	}
	var result batch.Checkpoint
	err := s.withTx(ctx, func(tx *gen.Tx) error {
		entity, err := tx.Checkpoint.Query().Where(checkpoint.JobKeyEQ(request.Grant.Resource)).ForUpdate().Only(ctx)
		if err != nil {
			if gen.IsNotFound(err) {
				return ErrCheckpointNotFound
			}
			return err
		}
		now, err := databaseNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkGrant(entity, request.Grant, now); err != nil {
			return err
		}
		current := fromEntity(entity)
		if entity.LastStepID == request.StepID && entity.Version == request.ExpectedVersion+1 {
			result = current
			return nil
		}
		if entity.Version != request.ExpectedVersion || entity.Status == string(batch.Completed) {
			return batch.ErrConflict
		}
		progress, err := step(gen.NewTxContext(ctx, tx), current)
		if err != nil {
			return err
		}
		if progress.Processed < 0 || progress.Processed > math.MaxInt64-entity.ProcessedCount {
			return batch.ErrInvalidProgress
		}
		updated := tx.Checkpoint.UpdateOneID(entity.ID).
			SetCursor(cloneBytes(progress.Cursor)).
			SetVersion(entity.Version + 1).
			SetLastStepID(request.StepID).
			SetProcessedCount(entity.ProcessedCount + progress.Processed).
			SetUpdatedAt(now)
		if progress.Done {
			updated.SetStatus(string(batch.Completed)).SetCompletedAt(now)
		} else {
			updated.SetStatus(string(batch.Running))
		}
		saved, err := updated.Save(ctx)
		if err != nil {
			return err
		}
		result = fromEntity(saved)
		return nil
	})
	if err != nil {
		return batch.Checkpoint{}, err
	}
	return result, nil
}

func (s *Store) withTx(ctx context.Context, fn func(*gen.Tx) error) (err error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(coordination.ErrAmbiguous, err)
	}
	committed = true
	return nil
}

func (s *Store) updateLease(ctx context.Context, grant coordination.Grant, ttl time.Duration, release bool) error {
	return s.withTx(ctx, func(tx *gen.Tx) error {
		entity, err := tx.Checkpoint.Query().Where(checkpoint.JobKeyEQ(grant.Resource)).ForUpdate().Only(ctx)
		if err != nil {
			if gen.IsNotFound(err) {
				return coordination.ErrLeaseLost
			}
			return err
		}
		now, err := databaseNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkGrant(entity, grant, now); err != nil {
			return err
		}
		if entity.Status == string(batch.Completed) && !release {
			return coordination.ErrLeaseLost
		}
		updated := tx.Checkpoint.UpdateOneID(entity.ID).SetUpdatedAt(now)
		if release {
			updated.SetOwner("").SetLeaseUntil(now)
			if entity.Status != string(batch.Completed) {
				updated.SetStatus(string(batch.Ready))
			}
		} else {
			if ttl <= 0 {
				return coordination.ErrInvalidConfig
			}
			updated.SetLeaseUntil(now.Add(ttl))
		}
		return updated.Exec(ctx)
	})
}

func databaseNow(ctx context.Context, tx *gen.Tx) (time.Time, error) {
	rows, err := tx.Client().QueryContext(ctx, "SELECT clock_timestamp()")
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, sql.ErrNoRows
	}
	var now time.Time
	if err := rows.Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), rows.Err()
}

type lease struct {
	store *Store
	grant coordination.Grant
	ttl   time.Duration
}

func (l *lease) Grant() coordination.Grant { return l.grant }

func (l *lease) Renew(ctx context.Context) error {
	return l.store.updateLease(ctx, l.grant, l.ttl, false)
}

func (l *lease) Release(ctx context.Context) error {
	return l.store.updateLease(ctx, l.grant, 0, true)
}

func checkGrant(entity *gen.Checkpoint, grant coordination.Grant, now time.Time) error {
	if entity.FenceAuthority != grant.Fence.Authority {
		return coordination.ErrAuthorityMismatch
	}
	if entity.Owner != grant.Owner || entity.Fence != grant.Fence.Value || !entity.LeaseUntil.After(now) || entity.Status != string(batch.Running) && entity.Status != string(batch.Completed) {
		return coordination.ErrLeaseLost
	}
	return nil
}

func fromEntity(entity *gen.Checkpoint) batch.Checkpoint {
	definition := batch.Definition{
		Key:              entity.JobKey,
		InputFingerprint: entity.InputFingerprint,
		CursorSchema:     entity.CursorSchema,
		Boundary:         cloneBytes(entity.Boundary),
	}
	return batch.Checkpoint{
		Definition:     definition,
		Cursor:         cloneBytes(entity.Cursor),
		Version:        entity.Version,
		LastStepID:     entity.LastStepID,
		ProcessedCount: entity.ProcessedCount,
		Status:         batch.Status(entity.Status),
		Owner:          entity.Owner,
		Fence:          coordination.FenceToken{Authority: entity.FenceAuthority, Value: entity.Fence},
		LeaseUntil:     entity.LeaseUntil,
		CreatedAt:      entity.CreatedAt,
		UpdatedAt:      entity.UpdatedAt,
		CompletedAt:    entity.CompletedAt,
	}
}

func sameDefinition(first, second batch.Definition) bool {
	if first.Key != second.Key || first.InputFingerprint != second.InputFingerprint || first.CursorSchema != second.CursorSchema || len(first.Boundary) != len(second.Boundary) {
		return false
	}
	for i := range first.Boundary {
		if first.Boundary[i] != second.Boundary[i] {
			return false
		}
	}
	return true
}

func cloneBytes(value []byte) []byte { return append([]byte(nil), value...) }
