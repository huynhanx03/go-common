package runtime

import (
	_ "unsafe" // for go:linkname
)

// NanoTime returns the current time in nanoseconds from a monotonic clock.
//
//go:linkname NanoTime runtime.nanotime
func NanoTime() int64

// CPUTicks is a faster alternative to NanoTime to measure time duration.
//
//go:linkname CPUTicks runtime.cputicks
func CPUTicks() int64
