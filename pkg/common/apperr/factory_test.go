package apperr

import (
	"errors"
	"testing"
)

func TestEntityFactory(t *testing.T) {
	cause := errors.New("pq: connection reset")
	errUser := For("user")

	cases := []struct {
		name     string
		got      *AppError
		wantCode int
		wantMsg  string
	}{
		{"not found", errUser.NotFound(cause), CodeNotFound, "user not found"},
		{"create failed", errUser.CreateFailed(cause), CodeDatabaseError, "failed to create user"},
		{"get failed", errUser.GetFailed(cause), CodeDatabaseError, "failed to get user"},
		{"update failed", errUser.UpdateFailed(cause), CodeDatabaseError, "failed to update user"},
		{"delete failed", errUser.DeleteFailed(cause), CodeDatabaseError, "failed to delete user"},
		{"save failed", errUser.SaveFailed(cause), CodeDatabaseError, "failed to save user"},
		{"process failed", errUser.ProcessFailed(cause), CodeInternalServer, "failed to process user"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", tc.got.Code, tc.wantCode)
			}
			if tc.got.Message != tc.wantMsg {
				t.Errorf("Message = %q, want %q", tc.got.Message, tc.wantMsg)
			}
			if !errors.Is(tc.got, cause) {
				t.Error("cause must stay reachable through the error chain")
			}
		})
	}
}

func TestErrorsAsThroughWrapping(t *testing.T) {
	// The service layer may add context with %w — the response layer must
	// still find the AppError.
	appErr := For("order").NotFound(errors.New("sql: no rows"))
	wrapped := errors.Join(errors.New("get order flow"), appErr)

	var got *AppError
	if !errors.As(wrapped, &got) || got.Code != CodeNotFound {
		t.Fatalf("errors.As through wrap failed, got %v", got)
	}
}

func TestWithDetails(t *testing.T) {
	details := map[string]any{"errors": []string{"name is required"}}
	err := New(CodeValidationFailed, "validation failed", nil).WithDetails(details)

	if err.Details == nil {
		t.Fatal("Details not attached")
	}
}
