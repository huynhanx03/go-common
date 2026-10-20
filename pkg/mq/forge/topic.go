package forge

import "sync"

// Topic wraps a single CommitLog (1 topic = 1 partition for monolith use).
type Topic struct {
	name string
	log  *CommitLog
	mu   sync.Mutex
	wake chan struct{}
}

// Name returns the topic name.
func (t *Topic) Name() string { return t.name }

// Log returns the underlying commit log.
func (t *Topic) Log() *CommitLog { return t.log }

// Close closes the topic's commit log.
func (t *Topic) Close() error { return t.log.Close() }

func (t *Topic) signal() {
	t.mu.Lock()
	close(t.wake)
	t.wake = make(chan struct{})
	t.mu.Unlock()
}

func (t *Topic) waitChannel() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wake
}
