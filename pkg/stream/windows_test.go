package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type windowResult struct {
	data       string
	offset     int64
	startLimit int
	final      bool
}

func TestScanWindowsStreamsOverlappingWindows(t *testing.T) {
	var got []windowResult
	err := ScanWindows(context.Background(), strings.NewReader("abcdefgh"), WindowOptions{
		ChunkSize: 3,
		Overlap:   2,
	}, func(window Window) error {
		got = append(got, windowResult{
			data:       string(window.Data),
			offset:     window.Offset,
			startLimit: window.StartLimit,
			final:      window.Final,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("ScanWindows() error = %v", err)
	}

	want := []windowResult{
		{data: "abc", offset: 0, startLimit: 1},
		{data: "bcdef", offset: 1, startLimit: 3},
		{data: "efgh", offset: 4, startLimit: 4, final: true},
	}
	if len(got) != len(want) {
		t.Fatalf("visited %d windows, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestScanWindowsValidationAndReaderFailures(t *testing.T) {
	t.Run("rejects invalid arguments and options", func(t *testing.T) {
		visit := func(Window) error { return nil }
		cases := []struct {
			name string
			run  func() error
			want error
		}{
			{"nil context", func() error { return ScanWindows(nil, strings.NewReader(""), WindowOptions{}, visit) }, ErrInvalidArgument},
			{"nil reader", func() error { return ScanWindows(context.Background(), nil, WindowOptions{}, visit) }, ErrInvalidArgument},
			{"nil visitor", func() error { return ScanWindows(context.Background(), strings.NewReader(""), WindowOptions{}, nil) }, ErrInvalidArgument},
			{"negative chunk", func() error {
				return ScanWindows(context.Background(), strings.NewReader(""), WindowOptions{ChunkSize: -1}, visit)
			}, ErrInvalidOptions},
			{"negative overlap", func() error {
				return ScanWindows(context.Background(), strings.NewReader(""), WindowOptions{ChunkSize: 1, Overlap: -1}, visit)
			}, ErrInvalidOptions},
			{"capacity overflow", func() error {
				return ScanWindows(context.Background(), strings.NewReader(""), WindowOptions{ChunkSize: int(^uint(0) >> 1), Overlap: 1}, visit)
			}, ErrInvalidOptions},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if err := tc.run(); !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
			})
		}
	})

	t.Run("delivers data with a reader error and preserves visitor error precedence", func(t *testing.T) {
		readErr := errors.New("read failed")
		visitErr := errors.New("visit failed")
		reader := &errorReader{data: []byte("abc"), err: readErr}
		var visited []windowResult
		err := ScanWindows(context.Background(), reader, WindowOptions{ChunkSize: 3}, func(window Window) error {
			visited = append(visited, windowResult{data: string(window.Data), startLimit: window.StartLimit, final: window.Final})
			return nil
		})
		if !errors.Is(err, readErr) {
			t.Fatalf("error = %v, want %v", err, readErr)
		}
		if len(visited) != 1 || visited[0] != (windowResult{data: "abc", startLimit: 3, final: true}) {
			t.Fatalf("visited = %+v, want final data window", visited)
		}

		err = ScanWindows(context.Background(), &errorReader{data: []byte("abc"), err: readErr}, WindowOptions{ChunkSize: 3}, func(Window) error {
			return visitErr
		})
		if !errors.Is(err, visitErr) {
			t.Fatalf("error = %v, want visitor error %v", err, visitErr)
		}
	})

	t.Run("handles EOF, default chunk size, no progress, and visitor stop", func(t *testing.T) {
		var emptyFinal bool
		if err := ScanWindows(context.Background(), bytes.NewReader(nil), WindowOptions{}, func(window Window) error {
			emptyFinal = window.Final && len(window.Data) == 0
			return nil
		}); err != nil {
			t.Fatalf("empty input error = %v", err)
		}
		if !emptyFinal {
			t.Fatal("empty input did not produce an empty final window")
		}

		var defaultData string
		if err := ScanWindows(context.Background(), strings.NewReader("small"), WindowOptions{}, func(window Window) error {
			defaultData = string(window.Data)
			return nil
		}); err != nil || defaultData != "small" {
			t.Fatalf("default chunk result = %q, error = %v", defaultData, err)
		}

		if err := ScanWindows(context.Background(), emptyReader{}, WindowOptions{ChunkSize: 1}, func(Window) error { return nil }); !errors.Is(err, io.ErrNoProgress) {
			t.Fatalf("no-progress error = %v, want %v", err, io.ErrNoProgress)
		}

		stopErr := errors.New("stop")
		err := ScanWindows(context.Background(), strings.NewReader("abcdef"), WindowOptions{ChunkSize: 3}, func(Window) error { return stopErr })
		if !errors.Is(err, stopErr) {
			t.Fatalf("visitor stop error = %v, want %v", err, stopErr)
		}
	})
}

func TestScanWindowsStopsWhenContextIsCanceled(t *testing.T) {
	t.Run("before the first read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := ScanWindows(ctx, strings.NewReader("x"), WindowOptions{ChunkSize: 1}, func(Window) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("during a read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader := cancelReader{cancel: cancel}
		if err := ScanWindows(ctx, reader, WindowOptions{ChunkSize: 1}, func(Window) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("after a read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader := cancelAfterReadReader{cancel: cancel}
		if err := ScanWindows(ctx, reader, WindowOptions{ChunkSize: 1}, func(Window) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("between windows", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		err := ScanWindows(ctx, strings.NewReader("abcdef"), WindowOptions{ChunkSize: 3}, func(Window) error {
			calls++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("error = %v, visitor calls = %d; want cancellation after first window", err, calls)
		}
	})

	t.Run("when overlap is larger than a chunk", func(t *testing.T) {
		var windows []windowResult
		err := ScanWindows(context.Background(), strings.NewReader("abcdef"), WindowOptions{ChunkSize: 2, Overlap: 3}, func(window Window) error {
			windows = append(windows, windowResult{data: string(window.Data), offset: window.Offset, startLimit: window.StartLimit, final: window.Final})
			return nil
		})
		if err != nil {
			t.Fatalf("ScanWindows() error = %v", err)
		}
		want := []windowResult{
			{data: "abcd", offset: 0, startLimit: 1},
			{data: "bcdef", offset: 1, startLimit: 2},
			{data: "def", offset: 3, startLimit: 3, final: true},
		}
		if !reflect.DeepEqual(windows, want) {
			t.Fatalf("windows = %+v, want overlap-composed windows", windows)
		}
	})
}

type errorReader struct {
	data []byte
	err  error
}

func (r *errorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

type cancelReader struct{ cancel context.CancelFunc }

func (r cancelReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, nil
}

type cancelAfterReadReader struct{ cancel context.CancelFunc }

func (r cancelAfterReadReader) Read(p []byte) (int, error) {
	p[0] = 'x'
	r.cancel()
	return 1, nil
}
