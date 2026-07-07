package apperr

// Business response codes — the contract clients branch on. The HTTP status
// is derived from the code's range (see response.GetHTTPCode), so a new code
// in the right range maps to the right status with zero extra wiring.
//
// Convention: [2 digits HTTP class][1 digit module][2 digits sequence]
//   - go-common owns the generic block xx0xx (module digit 0)
//   - each application allocates its own module digits, e.g. 1=user, 2=order:
//     44100 user not found, 49101 email already exists, 44200 order not found
const (
	// Success codes (20000-29999)
	CodeSuccess   = 20000 // Success
	CodeCreated   = 20001 // Resource created successfully
	CodeUpdated   = 20002 // Resource updated successfully
	CodeDeleted   = 20003 // Resource deleted successfully
	CodeRetrieved = 20004 // Resource retrieved successfully

	// Client error codes (40000-40999)
	CodeParamInvalid     = 40000 // Invalid parameters
	CodeValidationFailed = 40001 // Validation failed
	CodeBadRequest       = 40002 // Bad request
	CodeInvalidID        = 40003 // Invalid ID format
	CodeBodyTooLarge     = 40005 // Request body too large (maps to 413)

	// Authentication errors (41000-41999)
	CodeUnauthorized    = 41000 // Unauthorized
	CodeInvalidToken    = 41001 // Invalid token
	CodeTokenExpired    = 41002 // Token expired
	CodeInvalidPassword = 41003 // Invalid password
	CodeAccountNotFound = 41004 // Account not found (maps to 404)

	// Rate limiting (42900-42999)
	CodeTooManyRequests = 42900 // Too many requests

	// Authorization errors (43000-43999)
	CodeForbidden = 43000 // Forbidden

	// Not found errors (44000-44999)
	CodeNotFound = 44000 // Resource not found

	// Conflict errors (49000-49999)
	CodeConflict = 49000 // Conflict

	// Server error codes (50000-59999)
	CodeInternalServer = 50000 // Internal server error
	CodeDatabaseError  = 50001 // Database error
	CodeMongoDBError   = 50002 // MongoDB error
	CodeRedisError     = 50003 // Redis error
)
