package batch

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
)

// Config bounds one Run segment and each transactional page.
type Config struct {
	Lease            settings.Coordination
	MaxPages         int
	MaxCursorBytes   int
	MaxBoundaryBytes int
	PageTimeout      time.Duration
}

// RunOption changes an optional behavior for one Run call.
type RunOption func(*runOptions)

type runOptions struct {
	progressObserver func(context.Context, Result)
}

// WithProgressObserver observes a page only after its transaction is confirmed committed.
func WithProgressObserver(observer func(context.Context, Result)) RunOption {
	return func(options *runOptions) { options.progressObserver = observer }
}

// Runner executes bounded, serial segments over a durable checkpoint Store.
type Runner struct {
	store                 Store
	log                   *logger.Logger
	config                Config
	leases                *coordination.FencedClient
	leaseOperationTimeout time.Duration
}

// New creates a batch runner over an application-owned transactional Store.
func New(store Store, log *logger.Logger, config Config) (*Runner, error) {
	if store == nil || log == nil {
		return nil, ErrInvalidConfig
	}
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	leases, err := coordination.NewFenced(store, log, config.Lease)
	if err != nil {
		return nil, err
	}
	config.Lease = leases.Config()
	leaseOperationTimeout := time.Duration(config.Lease.OperationTimeoutMilliseconds) * time.Millisecond
	if config.PageTimeout >= leaseOperationTimeout {
		return nil, ErrInvalidConfig
	}
	return &Runner{store: store, log: log.Named("batch"), config: config, leases: leases, leaseOperationTimeout: leaseOperationTimeout}, nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.MaxPages < 0 || config.MaxCursorBytes < 0 || config.MaxBoundaryBytes < 0 || config.PageTimeout < 0 {
		return Config{}, ErrInvalidConfig
	}
	if config.MaxPages == 0 {
		config.MaxPages = defaultMaxPages
	}
	if config.MaxCursorBytes == 0 {
		config.MaxCursorBytes = defaultMaxCursorBytes
	}
	if config.MaxBoundaryBytes == 0 {
		config.MaxBoundaryBytes = defaultMaxBoundaryBytes
	}
	if config.PageTimeout == 0 {
		config.PageTimeout = defaultPageTimeout
	}
	if config.MaxPages <= 0 || config.MaxCursorBytes <= 0 || config.MaxBoundaryBytes <= 0 || config.PageTimeout <= 0 {
		return Config{}, ErrInvalidConfig
	}
	return config, nil
}

// Config returns the normalized runtime bounds.
func (r *Runner) Config() Config { return r.config }

// Run opens or resumes one checkpoint and commits at most MaxPages pages.
func (r *Runner) Run(ctx context.Context, definition Definition, step Step, options ...RunOption) (Result, error) {
	if ctx == nil || step == nil || !validDefinition(definition) {
		return Result{Key: definition.Key}, ErrInvalidConfig
	}
	definition = cloneDefinition(definition)
	if len(definition.Boundary) > r.config.MaxBoundaryBytes {
		return Result{Key: definition.Key}, ErrInvalidProgress
	}
	settings := runOptions{}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	startedAt := time.Now()

	checkpoint, err := r.open(ctx, definition)
	if err != nil {
		return Result{Key: definition.Key}, err
	}
	if err := validateCheckpoint(checkpoint, definition, r.config.MaxCursorBytes); err != nil {
		return Result{Key: definition.Key}, err
	}
	if checkpoint.Status == Completed {
		return resultFromCheckpoint(checkpoint), nil
	}

	err = r.leases.Do(ctx, definition.Key, func(leaseCtx context.Context, grant coordination.Grant) error {
		checkpoint, err = r.get(leaseCtx, definition, checkpoint)
		if err != nil || checkpoint.Status == Completed {
			return err
		}
		for range r.config.MaxPages {
			if checkpoint.Status == Completed {
				return nil
			}
			if checkpoint.Version < 0 || checkpoint.Version == math.MaxInt64 || checkpoint.ProcessedCount < 0 {
				return ErrInvalidProgress
			}
			updated, progress, err := r.applyPage(leaseCtx, definition, checkpoint, grant, step)
			if err != nil {
				if errors.Is(err, ErrCompleted) {
					completed, getErr := r.get(leaseCtx, definition, checkpoint)
					if getErr == nil && completed.Status == Completed {
						checkpoint = completed
						return nil
					}
					return errors.Join(err, getErr)
				}
				return err
			}
			checkpoint = updated
			result := resultFromCheckpoint(checkpoint)
			if settings.progressObserver != nil {
				settings.progressObserver(leaseCtx, result)
			}
			if progress.Done || checkpoint.Status == Completed {
				return nil
			}
		}
		return nil
	})

	result := resultFromCheckpoint(checkpoint)
	if err != nil {
		if errors.Is(err, ErrCompleted) {
			completed, getErr := r.get(ctx, definition, checkpoint)
			if getErr == nil && completed.Status == Completed {
				return resultFromCheckpoint(completed), nil
			}
			return result, errors.Join(err, getErr)
		}
		return result, err
	}
	result = resultFromCheckpoint(checkpoint)
	r.log.Debug(ctx, "batch segment finished",
		zap.String("job_key", result.Key),
		zap.Int64("version", result.Version),
		zap.Int64("processed_count", result.ProcessedCount),
		zap.Bool("done", result.Done),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	)
	return result, nil
}

func (r *Runner) open(ctx context.Context, definition Definition) (Checkpoint, error) {
	operationCtx, cancel := context.WithTimeout(ctx, r.leaseOperationTimeout)
	defer cancel()
	return r.store.Open(operationCtx, definition)
}

func (r *Runner) get(ctx context.Context, definition Definition, fallback Checkpoint) (Checkpoint, error) {
	operationCtx, cancel := context.WithTimeout(ctx, r.leaseOperationTimeout)
	defer cancel()
	checkpoint, err := r.store.Get(operationCtx, definition.Key)
	if err != nil {
		return fallback, err
	}
	if err := validateCheckpoint(checkpoint, definition, r.config.MaxCursorBytes); err != nil {
		return fallback, err
	}
	return checkpoint, nil
}

func (r *Runner) applyPage(ctx context.Context, definition Definition, checkpoint Checkpoint, grant coordination.Grant, step Step) (Checkpoint, Progress, error) {
	request := StepRequest{
		Grant:           grant,
		ExpectedVersion: checkpoint.Version,
		StepID:          stepID(grant.Owner, checkpoint.Version),
	}
	var progress Progress
	var hasProgress bool
	wrappedStep := func(stepCtx context.Context, current Checkpoint) (Progress, error) {
		if err := validateCheckpoint(current, definition, r.config.MaxCursorBytes); err != nil {
			return Progress{}, err
		}
		result, err := step(stepCtx, cloneCheckpoint(current))
		if err != nil {
			return Progress{}, err
		}
		result = cloneProgress(result)
		if err := validateProgress(current, result, r.config.MaxCursorBytes); err != nil {
			return Progress{}, err
		}
		progress = result
		hasProgress = true
		return cloneProgress(result), nil
	}

	for attempt := 0; attempt < 2; attempt++ {
		pageCtx, cancel := context.WithTimeout(ctx, r.config.PageTimeout)
		updated, err := r.store.Apply(pageCtx, request, wrappedStep)
		cancel()
		if err == nil {
			if !hasProgress {
				return checkpoint, Progress{}, ErrConflict
			}
			if err := validateApplied(checkpoint, updated, request, progress); err != nil {
				return checkpoint, Progress{}, err
			}
			return cloneCheckpoint(updated), progress, nil
		}
		if attempt != 0 || !errors.Is(err, coordination.ErrAmbiguous) || ctx.Err() != nil {
			return checkpoint, Progress{}, err
		}
	}
	return checkpoint, Progress{}, coordination.ErrAmbiguous
}

func validDefinition(definition Definition) bool {
	return definition.Key != "" && definition.InputFingerprint != "" && definition.CursorSchema != ""
}

func validateCheckpoint(checkpoint Checkpoint, definition Definition, maxCursorBytes int) error {
	if !sameDefinition(checkpoint.Definition, definition) {
		return ErrDefinitionConflict
	}
	if checkpoint.Version < 0 || checkpoint.ProcessedCount < 0 || len(checkpoint.Cursor) > maxCursorBytes {
		return ErrConflict
	}
	switch checkpoint.Status {
	case Ready, Running, Completed:
		return nil
	default:
		return ErrConflict
	}
}

func validateProgress(checkpoint Checkpoint, progress Progress, maxCursorBytes int) error {
	if progress.Processed < 0 || len(progress.Cursor) > maxCursorBytes || checkpoint.Version == math.MaxInt64 || progress.Processed > math.MaxInt64-checkpoint.ProcessedCount {
		return ErrInvalidProgress
	}
	if !progress.Done && bytes.Equal(checkpoint.Cursor, progress.Cursor) {
		return ErrInvalidProgress
	}
	return nil
}

func validateApplied(previous, updated Checkpoint, request StepRequest, progress Progress) error {
	if !sameDefinition(previous.Definition, updated.Definition) || updated.Version != request.ExpectedVersion+1 || updated.LastStepID != request.StepID || updated.ProcessedCount != previous.ProcessedCount+progress.Processed || !bytes.Equal(updated.Cursor, progress.Cursor) {
		return ErrConflict
	}
	if progress.Done && updated.Status != Completed || !progress.Done && updated.Status != Running {
		return ErrConflict
	}
	return nil
}

func sameDefinition(first, second Definition) bool {
	return first.Key == second.Key && first.InputFingerprint == second.InputFingerprint && first.CursorSchema == second.CursorSchema && bytes.Equal(first.Boundary, second.Boundary)
}

func cloneDefinition(definition Definition) Definition {
	definition.Boundary = append([]byte(nil), definition.Boundary...)
	return definition
}

func cloneProgress(progress Progress) Progress {
	progress.Cursor = append([]byte(nil), progress.Cursor...)
	return progress
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	checkpoint.Definition = cloneDefinition(checkpoint.Definition)
	checkpoint.Cursor = append([]byte(nil), checkpoint.Cursor...)
	if checkpoint.CompletedAt != nil {
		completedAt := *checkpoint.CompletedAt
		checkpoint.CompletedAt = &completedAt
	}
	return checkpoint
}

func resultFromCheckpoint(checkpoint Checkpoint) Result {
	return Result{Key: checkpoint.Definition.Key, Version: checkpoint.Version, ProcessedCount: checkpoint.ProcessedCount, Done: checkpoint.Status == Completed}
}

func stepID(owner string, version int64) string {
	return owner + "/" + strconv.FormatInt(version, 10)
}
