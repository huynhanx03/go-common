//go:build !darwin && !linux

package forge

import "os"

// mapReadOnly deliberately falls back to File.ReadAt on platforms where the
// package does not provide a proven mmap implementation.
func mapReadOnly(_ *os.File, _ int) ([]byte, error) {
	return nil, nil
}

func unmapReadOnly(_ []byte) error {
	return nil
}
