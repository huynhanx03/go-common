package response

import (
	"net/http"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

// httpCodeOverrides lists business codes whose HTTP status deviates from
// their range mapping below.
var httpCodeOverrides = map[int]int{
	apperr.CodeCreated:          http.StatusCreated,               // 201 within the 2xx range
	apperr.CodeValidationFailed: http.StatusUnprocessableEntity,   // 422 within the 400 range
	apperr.CodeBodyTooLarge:     http.StatusRequestEntityTooLarge, // 413 within the 400 range
	apperr.CodeAccountNotFound:  http.StatusNotFound,              // resource lookup miss, not an auth failure
}

// httpCodeRanges maps business-code ranges [min, max) to HTTP statuses.
// Any code an application defines within a range inherits its status —
// no registration needed (see the convention in apperr/codes.go).
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
