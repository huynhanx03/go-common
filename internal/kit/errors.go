package kit

import "errors"

var (
	ErrInvalidInput           = errors.New("invalid input")
	ErrInvalidPlan            = errors.New("invalid file plan")
	ErrInvalidModule          = errors.New("invalid module path")
	ErrInvalidSelection       = errors.New("unsupported stack selection")
	ErrInvalidOperation       = errors.New("unsupported operation")
	ErrMissingModule          = errors.New("module path is required")
	ErrMissingCommonVersion   = errors.New("go-common version is required for a development build")
	ErrInvalidCommonVersion   = errors.New("invalid go-common version")
	ErrMissingDomain          = errors.New("domain is required")
	ErrDeclined               = errors.New("operation cancelled")
	ErrInvalidProjectMetadata = errors.New("unsupported project metadata")
	ErrFileConflict           = errors.New("project file conflict")
	ErrUnsafePath             = errors.New("unsafe project path")
	ErrMutationLocked         = errors.New("another project mutation is in progress")
	ErrRecoveryRequired       = errors.New("an interrupted project mutation requires recovery")
	ErrRollbackFailed         = errors.New("project mutation rollback failed")
	ErrInvalidDomain          = errors.New("invalid domain name")
	ErrInvalidResource        = errors.New("invalid resource name or route")
	ErrDomainNotFound         = errors.New("domain does not exist")
	ErrDuplicateSchema        = errors.New("resource schema already exists")
	ErrDuplicateRoute         = errors.New("resource route already exists")
	ErrResourceConflict       = errors.New("resource already exists with different options")
	ErrUnsupportedOperation   = errors.New("operation is not supported by the generator")
)
