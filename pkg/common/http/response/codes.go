package response

import "net/http"

const (
	// Success codes (20000-29999)
	CodeSuccess   = 20000 // Success
	CodeCreated   = 20001 // Resource created successfully
	CodeUpdated   = 20002 // Resource updated successfully
	CodeDeleted   = 20003 // Resource deleted successfully
	CodeRetrieved = 20004 // Resource retrieved successfully

	// Client error codes (40000-49999)
	CodeParamInvalid     = 40000 // Invalid parameters
	CodeValidationFailed = 40001 // Validation failed
	CodeBadRequest       = 40002 // Bad request
	CodeInvalidID        = 40003 // Invalid ID format
	CodeInternalError    = 40004 // Internal error

	// Authentication/Authorization errors (41000-41999)
	CodeUnauthorized    = 41000 // Unauthorized
	CodeInvalidToken    = 41001 // Invalid token
	CodeTokenExpired    = 41002 // Token expired
	CodeInvalidPassword = 41003 // Invalid password
	CodeAccountNotFound = 41004 // Account not found
	CodeForbidden       = 43000 // Forbidden (403) - Note: standard is 403 but creating range 43000

	// Not found errors (44000-44999)
	CodeNotFound = 44000 // Resource not found

	// Rate limiting (42900-42999)
	CodeTooManyRequests = 42900 // Too many requests

	// Conflict errors (49000-49999)
	CodeConflict = 49000 // Conflict

	// Server error codes (50000-59999)
	CodeInternalServer = 50000 // Internal server error
	CodeDatabaseError  = 50001 // Database error
	CodeMongoDBError   = 50002 // MongoDB error
	CodeRedisError     = 50003 // Redis error
)

// httpCodeOverrides lists business codes whose HTTP status deviates from
// their range mapping below.
var httpCodeOverrides = map[int]int{
	CodeCreated:          http.StatusCreated,             // 201 within the 2xx range
	CodeValidationFailed: http.StatusUnprocessableEntity, // 422 within the 400 range
	CodeAccountNotFound:  http.StatusNotFound,            // resource lookup miss, not an auth failure
}

// httpCodeRanges maps business-code ranges [min, max) to HTTP statuses.
var httpCodeRanges = []struct {
	min, max, status int
}{
	{20000, 30000, http.StatusOK},
	{40000, 41000, http.StatusBadRequest},
	{41000, 42000, http.StatusUnauthorized},
	{42900, 43000, http.StatusTooManyRequests},
	{43000, 44000, http.StatusForbidden},
	{44000, 45000, http.StatusNotFound},
	{49000, 50000, http.StatusConflict},
	{50000, 60000, http.StatusInternalServerError},
}

// GetHTTPCode returns the standard HTTP status code for a given business
// code: specific overrides first, then the code's range, defaulting to 500.
func GetHTTPCode(code int) int {
	if status, ok := httpCodeOverrides[code]; ok {
		return status
	}
	for _, r := range httpCodeRanges {
		if code >= r.min && code < r.max {
			return r.status
		}
	}
	return http.StatusInternalServerError
}
