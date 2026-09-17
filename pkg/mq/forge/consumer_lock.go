package forge

import (
	"errors"
	"os"
	"sync"
)

type consumerLease struct {
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

func (lease *consumerLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.closeOnce.Do(func() {
		lease.closeErr = errors.Join(
			unlockConsumerFile(lease.file),
			lease.file.Close(),
		)
	})
	return lease.closeErr
}
