package runtime

import (
	_ "unsafe" // for go:linkname
)

// Uint32 returns a fast random uint32 value.
//
//go:linkname Uint32 runtime.fastrand
func Uint32() uint32

// Uint32n returns a fast random uint32 value in [0, n).
//
//go:linkname Uint32n runtime.fastrandn
func Uint32n(n uint32) uint32

// Unit64 returns a fast random uint64 value.
func Unit64() uint64 {
	v := uint64(Uint32())
	return v<<32 | uint64(Uint32())
}
