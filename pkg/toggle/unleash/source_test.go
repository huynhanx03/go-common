package unleash

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

type testSourceFunc func(context.Context, core.Scope) (core.Snapshot, error)

func (f testSourceFunc) Load(ctx context.Context, scope core.Scope) (core.Snapshot, error) {
	return f(ctx, scope)
}

func TestSourceTransportBootstrapsAndAcceptsOnlyNewerSnapshots(t *testing.T) {
	scope := core.Scope{Namespace: "billing", Environment: "prod"}
	initial := core.Snapshot{Scope: scope, Revision: 3, Flags: []core.Flag{{Key: "checkout", Enabled: true}}}
	var loaded core.Snapshot
	calls := 0
	transport := &sourceTransport{
		source: testSourceFunc(func(context.Context, core.Scope) (core.Snapshot, error) {
			calls++
			return loaded, nil
		}),
		scope: scope, timeout: time.Second, lifetime: context.Background(), accepted: initial, first: true,
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+sdkHost+sdkFeaturesPath, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK || calls != 0 {
		t.Fatalf("bootstrap response=%v loads=%d err=%v", response, calls, err)
	}
	_ = response.Body.Close()
	transport.first = false
	loaded = initial
	response, err = transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusNotModified || calls != 1 {
		t.Fatalf("unchanged response=%v loads=%d err=%v", response, calls, err)
	}
	_ = response.Body.Close()

	loaded = core.Snapshot{Scope: scope, Revision: 4, Flags: []core.Flag{{Key: "checkout", Enabled: false}}}
	response, err = transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK || transport.accepted.Revision != 4 || calls != 2 {
		t.Fatalf("newer response=%v accepted=%+v loads=%d err=%v", response, transport.accepted, calls, err)
	}
	_ = response.Body.Close()

	for _, stale := range []core.Snapshot{
		{Scope: scope, Revision: 3, Flags: []core.Flag{{Key: "checkout", Enabled: true}}},
		{Scope: scope, Revision: 4, Flags: []core.Flag{{Key: "checkout", Enabled: true}}},
	} {
		loaded = stale
		if response, err := transport.RoundTrip(request); response != nil || !errors.Is(err, core.ErrStaleRevision) {
			t.Fatalf("stale snapshot response=%v err=%v", response, err)
		}
	}
}

func TestSourceTransportRejectsInvalidRequestsAndSourceResults(t *testing.T) {
	scope := core.Scope{Namespace: "billing", Environment: "prod"}
	snapshot := core.Snapshot{Scope: scope, Revision: 1}
	request, err := http.NewRequest(http.MethodGet, "http://"+sdkHost+sdkFeaturesPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("storage credentials leaked")
	for _, test := range []struct {
		name   string
		source testSourceFunc
		want   error
	}{
		{name: "source failure", source: func(context.Context, core.Scope) (core.Snapshot, error) { return core.Snapshot{}, cause }, want: cause},
		{name: "invalid scope in snapshot", source: func(context.Context, core.Scope) (core.Snapshot, error) {
			return core.Snapshot{Scope: core.Scope{Namespace: "other", Environment: "prod"}, Revision: 1}, nil
		}, want: core.ErrInvalidDefinition},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &sourceTransport{source: test.source, scope: scope, timeout: time.Second, lifetime: context.Background(), accepted: snapshot}
			if response, err := transport.RoundTrip(request); response != nil || !errors.Is(err, core.ErrSource) || !errors.Is(err, test.want) {
				t.Fatalf("response=%v err=%v", response, err)
			}
		})
	}

	invalidRequest := request.Clone(context.Background())
	invalidRequest.Method = http.MethodPost
	transport := &sourceTransport{source: testSourceFunc(func(context.Context, core.Scope) (core.Snapshot, error) { return snapshot, nil }), scope: scope, timeout: time.Second, lifetime: context.Background(), accepted: snapshot}
	if response, err := transport.RoundTrip(invalidRequest); response != nil || !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("invalid request response=%v err=%v", response, err)
	}
}

func TestSourceTransportHonorsRequestTimeoutAndCancellation(t *testing.T) {
	scope := core.Scope{Namespace: "billing", Environment: "prod"}
	initial := core.Snapshot{Scope: scope, Revision: 1}
	request, err := http.NewRequest(http.MethodGet, "http://"+sdkHost+sdkFeaturesPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &sourceTransport{
		source: testSourceFunc(func(ctx context.Context, _ core.Scope) (core.Snapshot, error) {
			<-ctx.Done()
			return initial, nil
		}),
		scope: scope, timeout: time.Millisecond, lifetime: context.Background(), accepted: initial,
	}
	if response, err := transport.RoundTrip(request); response != nil || !errors.Is(err, core.ErrSource) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out source response=%v err=%v", response, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if response, err := transport.RoundTrip(request.Clone(canceled)); response != nil || !errors.Is(err, core.ErrSource) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request response=%v err=%v", response, err)
	}
}

func TestSourceTransportStopsWhenClientLifetimeEnds(t *testing.T) {
	scope := core.Scope{Namespace: "billing", Environment: "prod"}
	lifetime, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequest(http.MethodGet, "http://"+sdkHost+sdkFeaturesPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	transport := &sourceTransport{
		source: testSourceFunc(func(context.Context, core.Scope) (core.Snapshot, error) {
			loads++
			return core.Snapshot{Scope: scope, Revision: 1}, nil
		}),
		scope: scope, timeout: time.Second, lifetime: lifetime,
		accepted: core.Snapshot{Scope: scope, Revision: 1},
	}

	if response, err := transport.RoundTrip(request); response != nil || !errors.Is(err, core.ErrSource) || !errors.Is(err, context.Canceled) {
		t.Fatalf("closed transport response=%v err=%v", response, err)
	}
	if loads != 0 {
		t.Fatalf("closed transport loaded source %d times", loads)
	}
}
