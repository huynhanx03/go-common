package ent

import (
	"bytes"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

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
