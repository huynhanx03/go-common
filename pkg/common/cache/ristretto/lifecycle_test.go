package ristretto

import (
	"sync"
	"testing"
)

func TestCloseIsConcurrentIdempotentAndOperationsFailClosed(t *testing.T) {
	t.Parallel()

	cache, err := New[string, string]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cache.Close()
		}()
	}
	wait.Wait()
	if cache.Set("late", "value") {
		t.Fatal("Set succeeded after Close")
	}
	if _, found := cache.Get("late"); found {
		t.Fatal("Get found a value after Close")
	}
	cache.Delete("late")
	cache.Clear()
	_ = cache.Stats()
}
