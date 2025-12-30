# WorkerPool

A high-performance, low-cost goroutine pool for Go, designed to manage and recycle a massive number of goroutines, reducing memory consumption and preventing goroutine leaks.

## Prerequisites

- Go 1.18+

## Features

- **Automatic Goroutine Management**: Automatically manages the lifecycle of goroutines, recycling them for reuse.
- **Resource Saving**: Significantly reduces memory consumption compared to native goroutines.
- **Non-blocking Mechanism**: Prevents deadlock when the pool is full using non-blocking mode or max blocking tasks.
- **Periodic Purge**: Automatically cleans up expired/idle workers to free up resources.
- **Panic Handling**: Supports custom panic handlers to prevent program crashes from individual tasks.
- **Generic Support**: Provides type-safe `GenericPool[T]` for better developer experience and performance.

## Usage

### 1. Common Pool
Use `NewPool` to create a pool and `Submit` to dispatch tasks.

```go
package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/workerpool"
)

func main() {
	// Create a pool with capacity 10
	p, _ := workerpool.NewPool(10)
	defer p.Release()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		_ = p.Submit(func() {
			defer wg.Done()
			fmt.Println("Task running")
			time.Sleep(10 * time.Millisecond)
		})
	}
	wg.Wait()
}
```

### 2. Generic Pool
Use `NewGenericPool` when you need to pass specific typed arguments to workers.

```go
package main

import (
	"fmt"

	"github.com/huynhanx03/go-common/pkg/common/workerpool"
)

func main() {
	// Create a pool that accepts strings
	p, _ := workerpool.NewGenericPool(5, func(name string) {
		fmt.Printf("Hello, %s!\n", name)
	})
	defer p.Release()

	p.Invoke("Alice")
	p.Invoke("Bob")
}
```

## Configuration

You can customize the pool using functional options:

- `WithNonblocking(bool)`: Returns `ErrPoolOverload` immediately if pool is full, instead of blocking.
- `WithMaxBlockingTasks(int)`: Sets the maximum number of goroutines that can be blocked waiting for a worker.
- `WithExpiryDuration(time.Duration)`: Sets the duration after which idle workers are purged (default 1s).
- `WithPanicHandler(func(any))`: Sets a custom handler for panics occurred within tasks.
- `WithPreAlloc(bool)`: Pre-allocates the worker queue for slightly faster startup but higher initial memory.

## Reference

This package incorporates the core design principles and optimizations from [ants](https://github.com/panjf2000/ants) by Andy Pan.