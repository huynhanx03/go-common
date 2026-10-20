package sync

import "github.com/puzpuzpuz/xsync/v4"

var _ Counter = (*xsync.Counter)(nil)

func NewCounter() Counter { return xsync.NewCounter() }
