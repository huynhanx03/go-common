package response

// Msg maps codes to default messages
var Msg = map[int]string{
	// Success
	CodeSuccess:   "Success",
	CodeCreated:   "Resource created successfully",
	CodeUpdated:   "Resource updated successfully",
	CodeDeleted:   "Resource deleted successfully",
	CodeRetrieved: "Resource retrieved successfully",

	// Client errors
	CodeParamInvalid:     "Invalid parameters",
	CodeValidationFailed: "Validation failed",
	CodeBadRequest:       "Bad request",
	CodeInvalidID:        "Invalid ID format",

	// Authentication/Authorization
	CodeUnauthorized:    "Unauthorized",
	CodeInvalidToken:    "Invalid token",
	CodeTokenExpired:    "Token expired",
	CodeInvalidPassword: "Invalid password",
	CodeAccountNotFound: "Account not found",
	CodeForbidden:       "Forbidden",

	// Not found
	CodeNotFound: "Resource not found",

	// Conflict
	CodeConflict: "Conflict",

	// Server errors
	CodeInternalServer: "Internal server error",
	CodeDatabaseError:  "Database error",
	CodeMongoDBError:   "MongoDB error",
	CodeRedisError:     "Redis error",
}
