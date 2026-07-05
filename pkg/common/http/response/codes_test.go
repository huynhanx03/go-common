package response

import (
	"net/http"
	"testing"
)

func TestGetHTTPCode(t *testing.T) {
	cases := []struct {
		name string
		code int
		want int
	}{
		{"success", CodeSuccess, http.StatusOK},
		{"created override", CodeCreated, http.StatusCreated},
		{"updated", CodeUpdated, http.StatusOK},
		{"param invalid", CodeParamInvalid, http.StatusBadRequest},
		{"validation override", CodeValidationFailed, http.StatusUnprocessableEntity},
		{"unauthorized", CodeUnauthorized, http.StatusUnauthorized},
		{"token expired", CodeTokenExpired, http.StatusUnauthorized},
		{"account not found override", CodeAccountNotFound, http.StatusNotFound},
		{"forbidden", CodeForbidden, http.StatusForbidden},
		{"not found", CodeNotFound, http.StatusNotFound},
		{"too many requests", CodeTooManyRequests, http.StatusTooManyRequests},
		{"conflict", CodeConflict, http.StatusConflict},
		{"internal server", CodeInternalServer, http.StatusInternalServerError},
		{"database error", CodeDatabaseError, http.StatusInternalServerError},
		{"unlisted 2xx-range code", 20099, http.StatusOK},
		{"unlisted 401-range code", 41099, http.StatusUnauthorized},
		{"unmapped gap defaults to 500", 42000, http.StatusInternalServerError},
		{"unknown code defaults to 500", 99999, http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetHTTPCode(tc.code); got != tc.want {
				t.Errorf("GetHTTPCode(%d) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}
