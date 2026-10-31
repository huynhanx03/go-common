package toggle_test

import (
	"context"
	"fmt"

	"github.com/huynhanx03/go-common/pkg/toggle"
)

type managerExampleStore struct{ snapshot toggle.Snapshot }

func (s *managerExampleStore) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return toggle.Snapshot{}, err
	}
	if s.snapshot.Scope == (toggle.Scope{}) {
		s.snapshot.Scope = scope
	}
	return toggle.NormalizeSnapshot(scope, s.snapshot)
}

func (s *managerExampleStore) Put(ctx context.Context, scope toggle.Scope, flag toggle.Flag, expected int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.snapshot.Scope != scope || s.snapshot.Revision != expected {
		return 0, toggle.ErrConflict
	}
	for i := range s.snapshot.Flags {
		if s.snapshot.Flags[i].Key == flag.Key {
			s.snapshot.Flags[i] = flag
			s.snapshot.Revision++
			return s.snapshot.Revision, nil
		}
	}
	s.snapshot.Flags = append(s.snapshot.Flags, flag)
	s.snapshot.Revision++
	return s.snapshot.Revision, nil
}

func (s *managerExampleStore) Delete(ctx context.Context, scope toggle.Scope, key string, expected int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.snapshot.Scope != scope || s.snapshot.Revision != expected {
		return 0, toggle.ErrConflict
	}
	for i := range s.snapshot.Flags {
		if s.snapshot.Flags[i].Key == key {
			s.snapshot.Flags = append(s.snapshot.Flags[:i], s.snapshot.Flags[i+1:]...)
			s.snapshot.Revision++
			return s.snapshot.Revision, nil
		}
	}
	return 0, toggle.ErrNotFound
}

// ExampleManager shows the scope revision flow. Applications supply a durable
// Store such as pkg/toggle/ent; this example Store exists only in the test file.
func ExampleManager() {
	ctx := context.Background()
	scope := toggle.Scope{Namespace: "billing", Environment: "development"}
	store := &managerExampleStore{snapshot: toggle.Snapshot{Scope: scope}}
	manager, err := toggle.NewManager(store)
	if err != nil {
		fmt.Println("manager setup failed")
		return
	}

	revision, err := manager.Put(ctx, scope, toggle.Flag{Key: "checkout-v2", Enabled: true}, 0)
	if err != nil {
		fmt.Println("write failed")
		return
	}
	fmt.Println(revision)

	revision, err = manager.Put(ctx, scope, toggle.Flag{Key: "checkout-v2", Enabled: false}, revision)
	if err != nil {
		fmt.Println("update failed")
		return
	}
	fmt.Println(revision)

	revision, err = manager.Delete(ctx, scope, "checkout-v2", revision)
	if err != nil {
		fmt.Println("delete failed")
		return
	}
	fmt.Println(revision)
	// Output:
	// 1
	// 2
	// 3
}
