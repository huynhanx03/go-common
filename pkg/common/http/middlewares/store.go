package middlewares

import (
	"sync"

	"github.com/gin-gonic/gin"
)

const (
	enrichStoreKey = "__enrich_store"
	EnrichAbortKey = "__enrich_abort"
)

// RequestStore is a thread-safe key-value store for the enricher pipeline.
// One instance per request, shared across concurrent enrichers.
type RequestStore struct {
	mu   sync.RWMutex
	data map[string]any
}

func newRequestStore() *RequestStore {
	return &RequestStore{data: make(map[string]any)}
}

func (s *RequestStore) set(key string, val any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
}

func (s *RequestStore) get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func getOrInitStore(c *gin.Context) *RequestStore {
	if v, ok := c.Get(enrichStoreKey); ok {
		return v.(*RequestStore)
	}
	s := newRequestStore()
	c.Set(enrichStoreKey, s)
	return s
}

// Set writes a value into the request enricher store (safe for concurrent use).
func Set(c *gin.Context, key string, val any) {
	getOrInitStore(c).set(key, val)
}

// Get reads a typed value from the enricher store.
func Get[T any](c *gin.Context, key string) (T, bool) {
	v, ok := getOrInitStore(c).get(key)
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := v.(T)
	return typed, ok
}

// Abort signals a critical error from an enricher goroutine.
// Parallel will detect this after wg.Wait() and abort the request.
func Abort(c *gin.Context, err error) {
	Set(c, EnrichAbortKey, err)
}

// AbortError returns the abort error set by an enricher, if any.
func AbortError(c *gin.Context) (error, bool) {
	return Get[error](c, EnrichAbortKey)
}
