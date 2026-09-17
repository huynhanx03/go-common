package casbin

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

type Runtime struct {
	live    atomic.Pointer[liveSnapshot]
	desired atomic.Int64
	nonce   *runtimeNonce
	options runtimeOptions
}

func NewRuntime(initial Snapshot, optionFunctions ...Option) (*Runtime, error) {
	options := defaultRuntimeOptions()
	for _, option := range optionFunctions {
		if option == nil {
			return nil, ErrInvalidOption
		}
		if err := option(&options); err != nil {
			return nil, err
		}
	}
	live, err := prepareLiveSnapshot(context.Background(), initial, options.limits)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		nonce:   &runtimeNonce{marker: 1},
		options: options,
	}
	runtime.live.Store(live)
	runtime.desired.Store(initial.Revision)
	return runtime, nil
}

func (r *Runtime) Prepare(
	ctx context.Context,
	candidate Snapshot,
) (*PreparedSnapshot, error) {
	if r == nil || ctx == nil {
		return nil, ErrInvalidSnapshot
	}
	current := r.live.Load()
	if current == nil || candidate.Revision <= current.revision {
		return nil, ErrStaleRevision
	}
	prepared, err := prepareLiveSnapshot(ctx, candidate, r.options.limits)
	if err != nil {
		return nil, err
	}
	return &PreparedSnapshot{
		owner:    r.nonce,
		snapshot: prepared,
	}, nil
}

func (r *Runtime) Publish(prepared *PreparedSnapshot) error {
	if r == nil || prepared == nil || prepared.snapshot == nil {
		return ErrInvalidSnapshot
	}
	if prepared.owner != r.nonce {
		return ErrForeignPrepared
	}
	if !prepared.used.CompareAndSwap(false, true) {
		return ErrAlreadyPublished
	}
	for {
		current := r.live.Load()
		if current == nil || prepared.snapshot.revision <= current.revision {
			return ErrStaleRevision
		}
		if r.live.CompareAndSwap(current, prepared.snapshot) {
			return nil
		}
	}
}

func (r *Runtime) Authorize(
	ctx context.Context,
	request authorization.Request,
) (authorization.Decision, error) {
	live := r.current()
	revision := int64(0)
	if live != nil {
		revision = live.revision
	}
	if r == nil || ctx == nil {
		return denied(
			authorization.ReasonInvalidRequest,
			revision,
		), authorization.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), errors.Join(authorization.ErrEvaluation, err)
	}
	if err := request.Validate(r.options.clock().UTC()); err != nil {
		return denied(
			authorization.ReasonInvalidRequest,
			revision,
		), authorization.ErrInvalidRequest
	}
	if !request.Principal.Authenticated {
		return denied(authorization.ReasonAnonymous, revision), nil
	}
	if live == nil || live.enforcer == nil {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), authorization.ErrEvaluation
	}
	if !r.snapshotIsCurrent(live) {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), errors.Join(authorization.ErrEvaluation, ErrUnhealthy)
	}
	allowed, err := live.enforcer.Enforce(
		request.Principal.Subject,
		request.Resource,
		request.Action,
	)
	if err != nil {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), authorization.ErrEvaluation
	}
	if err := ctx.Err(); err != nil {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), errors.Join(authorization.ErrEvaluation, err)
	}
	// Re-check the fence after evaluation. If persistence advanced or another
	// snapshot was published concurrently, a decision from the previous
	// snapshot is not returned as a post-commit authorization result.
	if !r.snapshotIsCurrent(live) {
		return denied(
			authorization.ReasonEvaluationError,
			revision,
		), errors.Join(authorization.ErrEvaluation, ErrUnhealthy)
	}
	if !allowed {
		return denied(authorization.ReasonPolicyDenied, revision), nil
	}
	return authorization.Decision{
		Allowed:  true,
		Reason:   authorization.ReasonAllowed,
		Revision: revision,
	}, nil
}

func (r *Runtime) snapshotIsCurrent(snapshot *liveSnapshot) bool {
	if r == nil || snapshot == nil || snapshot.revision <= 0 {
		return false
	}
	return r.live.Load() == snapshot && r.desired.Load() == snapshot.revision
}

func (r *Runtime) AppliedRevision() int64 {
	live := r.current()
	if live == nil {
		return 0
	}
	return live.revision
}

// DesiredRevision returns the application-published persistence fence. A
// caller can expose desired/applied state to readiness or administration
// without reaching into the runtime implementation.
func (r *Runtime) DesiredRevision() int64 {
	if r == nil {
		return 0
	}
	return r.desired.Load()
}

func (r *Runtime) SetDesiredRevision(revision int64) {
	if r == nil {
		return
	}
	r.desired.Store(revision)
}

// CompareAndSwapDesiredRevision conditionally moves the persistence fence.
// It is primarily useful for restoring an earlier revision after a database
// transaction rolls back without clobbering a newer concurrent mutation.
func (r *Runtime) CompareAndSwapDesiredRevision(oldRevision, newRevision int64) bool {
	if r == nil || oldRevision <= 0 || newRevision <= 0 {
		return false
	}
	return r.desired.CompareAndSwap(oldRevision, newRevision)
}

func (r *Runtime) Healthy() error {
	if r == nil {
		return ErrUnhealthy
	}
	applied := r.AppliedRevision()
	desired := r.desired.Load()
	if applied <= 0 || desired <= 0 || applied != desired {
		return ErrUnhealthy
	}
	return nil
}

func (r *Runtime) current() *liveSnapshot {
	if r == nil {
		return nil
	}
	return r.live.Load()
}

func denied(reason string, revision int64) authorization.Decision {
	return authorization.Decision{
		Allowed:  false,
		Reason:   reason,
		Revision: revision,
	}
}

var _ authorization.Authorizer = (*Runtime)(nil)
