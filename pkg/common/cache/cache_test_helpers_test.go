package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type memoryL1 struct {
	mu       sync.Mutex
	values   map[string]any
	versions map[string]string
	errors   map[string]error
	closed   atomic.Int32
	writes   atomic.Int32
}

type delayedNegativeL1 struct {
	*memoryL1
	negativeKey string
	calls       atomic.Int32
}

func (m *delayedNegativeL1) Get(ctx context.Context, key string) (any, bool) {
	if key == m.negativeKey && m.calls.Add(1) == 1 {
		return nil, false
	}
	return m.memoryL1.Get(ctx, key)
}

func newMemoryL1() *memoryL1 {
	return &memoryL1{values: map[string]any{}, versions: map[string]string{}, errors: map[string]error{}}
}
func (m *memoryL1) Get(_ context.Context, key string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	return v, ok
}
func (m *memoryL1) SetWithTTL(_ context.Context, key string, value any, _ time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return true
}
func (m *memoryL1) SnapshotVersion(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["snapshot"]; err != nil {
		return "", err
	}
	if m.versions[key] == "" {
		m.versions[key] = "v1"
	}
	return m.versions[key], nil
}
func (m *memoryL1) SetWithVersion(_ context.Context, versionKey, storageKey string, value any, _ time.Duration, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["set"]; err != nil {
		return err
	}
	if m.versions[versionKey] == token {
		m.values[storageKey] = value
		m.writes.Add(1)
	}
	return nil
}
func (m *memoryL1) DeleteValueWithVersion(_ context.Context, versionKey, storageKey, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["delete"]; err != nil {
		return err
	}
	if m.versions[versionKey] == token {
		delete(m.values, storageKey)
	}
	return nil
}
func (m *memoryL1) Revoke(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["revoke"]; err != nil {
		return err
	}
	m.next(key)
	return nil
}
func (m *memoryL1) RevokeMany(ctx context.Context, keys ...string) error {
	if err := m.errors["revoke_many"]; err != nil {
		return err
	}
	for _, key := range keys {
		if err := m.Revoke(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
func (m *memoryL1) Close() { m.closed.Add(1) }
func (m *memoryL1) next(key string) {
	m.versions[key] = fmt.Sprintf("v%d", len(m.versions)+1)
	delete(m.values, key)
	delete(m.values, key+NegativeKeySuffix)
}

type memoryL2 struct {
	mu       sync.Mutex
	values   map[string][]byte
	versions map[string]string
	errors   map[string]error
	closed   atomic.Int32
}

func newMemoryL2() *memoryL2 {
	return &memoryL2{values: map[string][]byte{}, versions: map[string]string{}, errors: map[string]error{}}
}
func (m *memoryL2) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["get"]; err != nil {
		return nil, false, err
	}
	v, ok := m.values[key]
	return append([]byte(nil), v...), ok, nil
}
func (m *memoryL2) SnapshotVersion(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["snapshot"]; err != nil {
		return "", err
	}
	if m.versions[key] == "" {
		m.versions[key] = "v1"
	}
	return m.versions[key], nil
}
func (m *memoryL2) SetWithVersion(_ context.Context, versionKey, storageKey string, value []byte, _ time.Duration, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["set"]; err != nil {
		return err
	}
	if m.versions[versionKey] == token {
		m.values[storageKey] = append([]byte(nil), value...)
	}
	return nil
}
func (m *memoryL2) DeleteValueWithVersion(_ context.Context, versionKey, storageKey, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["delete"]; err != nil {
		return err
	}
	if m.versions[versionKey] == token {
		delete(m.values, storageKey)
	}
	return nil
}
func (m *memoryL2) Revoke(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["revoke"]; err != nil {
		return err
	}
	m.next(key)
	return nil
}
func (m *memoryL2) RevokeMany(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.errors["revoke_many"]; err != nil {
		return err
	}
	for _, key := range keys {
		m.next(key)
	}
	return nil
}
func (m *memoryL2) Close() { m.closed.Add(1) }
func (m *memoryL2) next(key string) {
	m.versions[key] = fmt.Sprintf("v%d", len(m.versions)+1)
	delete(m.values, key)
	delete(m.values, key+NegativeKeySuffix)
}

func newTestCache(t *testing.T, l1 L1Engine, l2 L2Engine) *Cache {
	t.Helper()
	c, err := New(Config{L1: l1, L2: l2, Logger: logger.Wrap(zap.NewNop())})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
