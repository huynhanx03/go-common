package response

import "github.com/huynhanx03/go-common/pkg/common/apperr"

// Msg maps codes to default messages, used when an AppError carries no
// specific message of its own.
var Msg = map[int]string{
	// Success
	apperr.CodeSuccess:   "Success",
	apperr.CodeCreated:   "Resource created successfully",
	apperr.CodeUpdated:   "Resource updated successfully",
	apperr.CodeDeleted:   "Resource deleted successfully",
	apperr.CodeRetrieved: "Resource retrieved successfully",

	// Client errors
	apperr.CodeParamInvalid:     "Invalid parameters",
	apperr.CodeValidationFailed: "Validation failed",
	apperr.CodeBadRequest:       "Bad request",
	apperr.CodeInvalidID:        "Invalid ID format",
	apperr.CodeBodyTooLarge:     "Request body too large",

	// Authentication/Authorization
	apperr.CodeUnauthorized:    "Unauthorized",
	apperr.CodeInvalidToken:    "Invalid token",
	apperr.CodeTokenExpired:    "Token expired",
	apperr.CodeInvalidPassword: "Invalid password",
	apperr.CodeAccountNotFound: "Account not found",
	apperr.CodeForbidden:       "Forbidden",

	// Not found
	apperr.CodeNotFound: "Resource not found",

	// Rate limiting
	apperr.CodeTooManyRequests: "Too many requests",

	// Conflict
	apperr.CodeConflict: "Conflict",

	// Server errors
	apperr.CodeInternalServer: "Internal server error",
	apperr.CodeDatabaseError:  "Database error",
	apperr.CodeMongoDBError:   "MongoDB error",
	apperr.CodeRedisError:     "Redis error",
}
