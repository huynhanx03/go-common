package response

import (
	"net/http"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

func TestGetHTTPCode(t *testing.T) {
	cases := []struct {
		name string
		code int
		want int
	}{
		{"success", apperr.CodeSuccess, http.StatusOK},
		{"created override", apperr.CodeCreated, http.StatusCreated},
		{"updated", apperr.CodeUpdated, http.StatusOK},
		{"param invalid", apperr.CodeParamInvalid, http.StatusBadRequest},
		{"validation override", apperr.CodeValidationFailed, http.StatusUnprocessableEntity},
		{"body too large override", apperr.CodeBodyTooLarge, http.StatusRequestEntityTooLarge},
		{"unauthorized", apperr.CodeUnauthorized, http.StatusUnauthorized},
		{"token expired", apperr.CodeTokenExpired, http.StatusUnauthorized},
		{"account not found override", apperr.CodeAccountNotFound, http.StatusNotFound},
		{"forbidden", apperr.CodeForbidden, http.StatusForbidden},
		{"not found", apperr.CodeNotFound, http.StatusNotFound},
		{"too many requests", apperr.CodeTooManyRequests, http.StatusTooManyRequests},
		{"conflict", apperr.CodeConflict, http.StatusConflict},
		{"internal server", apperr.CodeInternalServer, http.StatusInternalServerError},
		{"database error", apperr.CodeDatabaseError, http.StatusInternalServerError},
		{"app-defined 404-range code", 44100, http.StatusNotFound},
		{"app-defined conflict-range code", 49101, http.StatusConflict},
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
