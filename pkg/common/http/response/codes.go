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

	// Conflict errors (49000-49999)
	CodeConflict = 49000 // Conflict

	// Server error codes (50000-59999)
	CodeInternalServer = 50000 // Internal server error
	CodeDatabaseError  = 50001 // Database error
	CodeMongoDBError   = 50002 // MongoDB error
	CodeRedisError     = 50003 // Redis error
)

// GetHTTPCode returns the standard HTTP status code for a given business code
func GetHTTPCode(code int) int {
	// 1. Exact Match for Specific RFC Codes
	switch code {
	case CodeSuccess, CodeUpdated, CodeDeleted, CodeRetrieved:
		return http.StatusOK // 200
	case CodeCreated:
		return http.StatusCreated // 201
	case CodeParamInvalid, CodeBadRequest, CodeInvalidID, CodeInternalError:
		return http.StatusBadRequest // 400
	case CodeUnauthorized, CodeInvalidToken, CodeTokenExpired, CodeInvalidPassword:
		return http.StatusUnauthorized // 401
	case CodeForbidden, CodeAccountNotFound:
		// AccountNotFound can be 404, but often for auth it's 401/403 or 404.
		// Let's stick to NotFound for AccountNotFound if it's resource lookup,
		// or Unauthorized if login.
		// Assuming CodeAccountNotFound is resource:
		if code == CodeAccountNotFound {
			return http.StatusNotFound
		}
		return http.StatusForbidden // 403
	case CodeNotFound:
		return http.StatusNotFound // 404
	case CodeConflict:
		return http.StatusConflict // 409
	case CodeValidationFailed:
		return http.StatusUnprocessableEntity // 422
	case CodeInternalServer, CodeDatabaseError, CodeMongoDBError, CodeRedisError:
		return http.StatusInternalServerError // 500
	}

	// 2. Fallback Range Mapping
	switch {
	case code >= 20000 && code < 30000:
		return http.StatusOK
	case code >= 40000 && code < 41000:
		return http.StatusBadRequest
	case code >= 41000 && code < 42000:
		return http.StatusUnauthorized
	case code >= 43000 && code < 44000:
		return http.StatusForbidden
	case code >= 44000 && code < 45000:
		return http.StatusNotFound
	case code >= 49000 && code < 50000:
		return http.StatusConflict
	case code >= 50000 && code < 60000:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
