package casbin

import "errors"

var (
	ErrInvalidSnapshot  = errors.New("invalid snapshot")
	ErrInvalidRelations = errors.New("invalid authorization relations")
	ErrInvalidOption    = errors.New("invalid option")
	ErrForeignPrepared  = errors.New("prepared snapshot belongs to another runtime")
	ErrAlreadyPublished = errors.New("prepared snapshot already used")
	ErrStaleRevision    = errors.New("stale revision")
	ErrUnhealthy        = errors.New("desired revision is not applied")
	ErrInvalidCache     = errors.New("invalid runtime cache")
)
