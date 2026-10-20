package sync

import "github.com/puzpuzpuz/xsync/v4"

var (
	_ MPMCQueue[any]  = (*xsync.MPMCQueue[any])(nil)
	_ SPSCQueue[any]  = (*xsync.SPSCQueue[any])(nil)
	_ UMPSCQueue[any] = (*xsync.UMPSCQueue[any])(nil)
)

func NewMPMCQueue[T any](capacity int) MPMCQueue[T] {
	return xsync.NewMPMCQueue[T](capacity)
}

func NewSPSCQueue[T any](capacity int) SPSCQueue[T] {
	return xsync.NewSPSCQueue[T](capacity)
}

func NewUMPSCQueue[T any]() UMPSCQueue[T] {
	return xsync.NewUMPSCQueue[T]()
}
