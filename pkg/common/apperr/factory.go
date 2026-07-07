package apperr

// Entity builds AppErrors scoped to one entity name, so services wrap errors
// in a single word instead of repeating code + format string at every site:
//
//	var errUser = apperr.For("user")
//	...
//	if err != nil {
//		return nil, errUser.CreateFailed(err)
//	}
//
// Each method pairs a fixed code with a fixed message — there is no format
// string to mis-fill. Cases outside this set (domain-specific codes, custom
// messages) use apperr.New directly.
//
// Call the methods with a non-nil err only: they are for wrapping a failure
// that already happened, and wrapping nil would fabricate an error.
type Entity string

// For returns an error factory scoped to the given entity name.
func For(entity string) Entity { return Entity(entity) }

// NotFound wraps err as "<entity> not found" (HTTP 404).
func (e Entity) NotFound(err error) *AppError {
	return New(CodeNotFound, string(e)+" not found", err)
}

// CreateFailed wraps err as "failed to create <entity>" (HTTP 500).
func (e Entity) CreateFailed(err error) *AppError {
	return New(CodeDatabaseError, "failed to create "+string(e), err)
}

// GetFailed wraps err as "failed to get <entity>" (HTTP 500).
func (e Entity) GetFailed(err error) *AppError {
	return New(CodeDatabaseError, "failed to get "+string(e), err)
}

// UpdateFailed wraps err as "failed to update <entity>" (HTTP 500).
func (e Entity) UpdateFailed(err error) *AppError {
	return New(CodeDatabaseError, "failed to update "+string(e), err)
}

// DeleteFailed wraps err as "failed to delete <entity>" (HTTP 500).
func (e Entity) DeleteFailed(err error) *AppError {
	return New(CodeDatabaseError, "failed to delete "+string(e), err)
}

// SaveFailed wraps err as "failed to save <entity>" (HTTP 500).
func (e Entity) SaveFailed(err error) *AppError {
	return New(CodeDatabaseError, "failed to save "+string(e), err)
}

// ProcessFailed wraps err as "failed to process <entity>" (HTTP 500).
func (e Entity) ProcessFailed(err error) *AppError {
	return New(CodeInternalServer, "failed to process "+string(e), err)
}
