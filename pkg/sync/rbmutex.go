package sync

import "github.com/puzpuzpuz/xsync/v4"

func NewRBMutex() RBMutex { return &rbMutexAdapter{RBMutex: xsync.NewRBMutex()} }

var _ RBMutex = (*rbMutexAdapter)(nil)

type rbMutexAdapter struct{ *xsync.RBMutex }

func (mu *rbMutexAdapter) RLock() *RToken {
	token := mu.RBMutex.RLock()
	if token == nil {
		return nil
	}
	return &RToken{value: token}
}

func (mu *rbMutexAdapter) RUnlock(token *RToken) {
	if token == nil {
		mu.RBMutex.RUnlock(nil)
		return
	}
	mu.RBMutex.RUnlock(token.value.(*xsync.RToken))
}

func (mu *rbMutexAdapter) TryRLock() (bool, *RToken) {
	locked, token := mu.RBMutex.TryRLock()
	if token == nil {
		return locked, nil
	}
	return locked, &RToken{value: token}
}
