package runtime

import (
	_ "unsafe" // for go:linkname
)

// Procyield spins for a given number of cycles without yielding to the scheduler.
// It uses the CPU PAUSE instruction on x86 to reduce power consumption during spinning.
// cycles: number of spin iterations (typically 4-30 for short waits).
//
//go:linkname Procyield runtime.procyield
func Procyield(cycles uint32)
