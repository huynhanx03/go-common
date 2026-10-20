//go:build darwin || linux

package forge

import (
	"os"
	"syscall"
)

func mapReadOnly(file *os.File, size int) ([]byte, error) {
	return syscall.Mmap(
		int(file.Fd()),
		0,
		size,
		syscall.PROT_READ,
		syscall.MAP_SHARED,
	)
}

func unmapReadOnly(data []byte) error {
	return syscall.Munmap(data)
}
