package workerpool

import (
	"context"
	"time"

	"github.com/panjf2000/ants/v2"
)

// The functions below operate on the package-wide default pool, an unbounded
// pool shared by all callers that just want "go with reuse" semantics.

// Submit submits a task to the default pool.
func Submit(task func()) error {
	return ants.Submit(task)
}

// Running returns the number of the currently running workers in the default pool.
func Running() int {
	return ants.Running()
}

// Cap returns the capacity of the default pool.
func Cap() int {
	return ants.Cap()
}

// Free returns the number of available workers in the default pool.
func Free() int {
	return ants.Free()
}

// Release closes the default pool.
func Release() {
	ants.Release()
}

// ReleaseTimeout closes the default pool and waits until all workers exit or the timeout elapses.
func ReleaseTimeout(timeout time.Duration) error {
	return ants.ReleaseTimeout(timeout)
}

// ReleaseContext closes the default pool and waits until all workers exit or the context is done.
func ReleaseContext(ctx context.Context) error {
	return ants.ReleaseContext(ctx)
}

// Reboot reboots the default pool after a Release.
func Reboot() {
	ants.Reboot()
}
