package casbin

import "errors"

var (
	ErrInvalidSnapshot  = errors.New("authorization casbin: invalid snapshot")
	ErrInvalidOption    = errors.New("authorization casbin: invalid option")
	ErrForeignPrepared  = errors.New("authorization casbin: prepared snapshot belongs to another runtime")
	ErrAlreadyPublished = errors.New("authorization casbin: prepared snapshot already used")
	ErrStaleRevision    = errors.New("authorization casbin: stale revision")
	ErrUnhealthy        = errors.New("authorization casbin: desired revision is not applied")
)
