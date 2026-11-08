package ent

import (
	"bytes"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

type generatedNotFoundError struct{}

func (generatedNotFoundError) Error() string { return "generated record missing" }

func TestMapEntErrorDoesNotLogAtMappingBoundary(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "deadlock constraint",
			err:  &ConstraintError{msg: "deadlock contains-a-secret"},
		},
		{
			name: "unloaded edge",
			err:  &NotLoadedError{edge: "contains-a-secret"},
		},
		{
			name: "unexpected database error",
			err:  errors.New("database DSN contains-a-secret"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if mapped := MapEntError(test.err, "record"); mapped == nil {
				t.Fatal("MapEntError() returned nil for a non-nil error")
			}
		})
	}

	if got := logs.String(); got != "" {
		t.Fatalf("MapEntError() emitted an implicit log; logging belongs at the context-aware boundary:\n%s", got)
	}
}

func TestErrorPredicateRegistrationIsConcurrentSafe(t *testing.T) {
	const goroutines = 16
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wait.Add(1)
		go func(register bool) {
			defer wait.Done()
			<-start
			if register {
				RegisterErrorPredicates(ErrorPredicates{
					IsNotFound: func(error) bool { return false },
				})
				return
			}
			if mapped := MapEntError(errors.New("database failure"), "record"); mapped == nil {
				t.Error("MapEntError returned nil")
			}
		}(index%2 == 0)
	}
	close(start)
	wait.Wait()
}

func TestMapEntErrorUsesBusinessClassification(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		want  int
		isNil bool
	}{
		{name: "nil", isNil: true},
		{name: "not found", err: &NotFoundError{label: "account"}, want: apperr.CodeNotFound},
		{name: "validation", err: &ValidationError{err: errors.New("invalid field")}, want: apperr.CodeValidationFailed},
		{name: "unique constraint", err: &ConstraintError{msg: "unique index"}, want: apperr.CodeConflict},
		{name: "invalid foreign key", err: &ConstraintError{msg: "foreign key create failed"}, want: apperr.CodeBadRequest},
		{name: "referenced row mutation", err: &ConstraintError{msg: "foreign key update failed"}, want: apperr.CodeConflict},
		{name: "deadlock", err: &ConstraintError{msg: "deadlock detected"}, want: apperr.CodeDatabaseError},
		{name: "other constraint", err: &ConstraintError{msg: "check violation"}, want: apperr.CodeConflict},
		{name: "unloaded edge", err: &NotLoadedError{edge: "owner"}, want: apperr.CodeInternalServer},
		{name: "non singular result", err: &NotSingularError{label: "account"}, want: apperr.CodeBadRequest},
		{name: "unknown database failure", err: errors.New("driver failed"), want: apperr.CodeDatabaseError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := MapEntError(test.err, "account")
			if test.isNil {
				if mapped != nil {
					t.Fatalf("MapEntError(nil) = %+v", mapped)
				}
				return
			}
			if mapped == nil || mapped.Code != test.want {
				t.Fatalf("mapped error = %+v; want code %d", mapped, test.want)
			}
			if !errors.Is(mapped, test.err) {
				t.Fatal("mapped error did not preserve the database cause")
			}
		})
	}
}

func TestMapEntErrorRecognizesGeneratedEntErrors(t *testing.T) {
	RegisterErrorPredicates(ErrorPredicates{
		IsNotFound: func(err error) bool {
			_, ok := err.(generatedNotFoundError)
			return ok
		},
	})

	cause := generatedNotFoundError{}
	mapped := MapEntError(cause, "account")
	if mapped == nil || mapped.Code != apperr.CodeNotFound || !errors.Is(mapped, cause) {
		t.Fatalf("generated not-found mapping = %+v", mapped)
	}
}
