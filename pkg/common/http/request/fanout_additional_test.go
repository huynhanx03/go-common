package request

import (
	"context"
	"errors"
	"testing"
)

func TestFanoutFirstSuccessAndAllFailed(t *testing.T) {
	t.Parallel()

	t.Run("first success", func(t *testing.T) {
		t.Parallel()

		failure := errors.New("failed")
		tasks := []func(context.Context) (int, error){
			func(context.Context) (int, error) { return 0, failure },
			func(context.Context) (int, error) { return 42, nil },
			func(ctx context.Context) (int, error) {
				<-ctx.Done()
				return 0, ctx.Err()
			},
		}
		results, err := Fanout(context.Background(), FanoutOptions{
			MaxConcurrency: 3,
			Mode:           FanoutFirstSuccess,
		}, tasks...)
		if err != nil {
			t.Fatalf("Fanout: %v", err)
		}
		if len(results) != len(tasks) || !results[1].Started || results[1].Value != 42 || results[1].Err != nil {
			t.Fatalf("results = %+v", results)
		}
	})

	t.Run("all failed", func(t *testing.T) {
		t.Parallel()

		first := errors.New("first")
		second := errors.New("second")
		results, err := Fanout(context.Background(), FanoutOptions{
			MaxConcurrency: 1,
			Mode:           FanoutFirstSuccess,
		},
			func(context.Context) (int, error) { return 0, first },
			func(context.Context) (int, error) { return 0, second },
		)
		if len(results) != 2 ||
			!errors.Is(err, ErrNoSuccessfulResult) ||
			!errors.Is(err, first) ||
			!errors.Is(err, second) {
			t.Fatalf("results=%+v error=%v", results, err)
		}
	})
}

func TestFanoutContextTaskAndEmptyContracts(t *testing.T) {
	t.Parallel()

	t.Run("pre-canceled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		results, err := Fanout(ctx, FanoutOptions{
			MaxConcurrency: 1,
			Mode:           FanoutAll,
		}, func(context.Context) (int, error) {
			t.Fatal("task must not start")
			return 0, nil
		})
		if len(results) != 1 || results[0].Started || !errors.Is(err, context.Canceled) {
			t.Fatalf("results=%+v error=%v", results, err)
		}
	})

	t.Run("nil task", func(t *testing.T) {
		t.Parallel()

		results, err := Fanout(context.Background(), FanoutOptions{
			MaxConcurrency: 1,
			Mode:           FanoutFirstError,
		}, (func(context.Context) (int, error))(nil))
		if !errors.Is(err, ErrInvalidTask) || len(results) != 1 || !errors.Is(results[0].Err, ErrInvalidTask) {
			t.Fatalf("results=%+v error=%v", results, err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		results, err := Fanout[int](context.Background(), FanoutOptions{
			MaxConcurrency: 1,
			Mode:           FanoutAll,
		})
		if err != nil || results == nil || len(results) != 0 {
			t.Fatalf("results=%+v error=%v", results, err)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		t.Parallel()

		_, err := Fanout[int](nil, FanoutOptions{
			MaxConcurrency: 1,
			Mode:           FanoutAll,
		})
		if !errors.Is(err, ErrInvalidFanout) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestFanoutFirstErrorWithoutFailure(t *testing.T) {
	t.Parallel()

	results, err := Fanout(context.Background(), FanoutOptions{
		MaxConcurrency: 1,
		Mode:           FanoutFirstError,
	},
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context) (int, error) { return 2, nil },
	)
	if err != nil || len(results) != 2 || results[0].Value != 1 || results[1].Value != 2 {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}
