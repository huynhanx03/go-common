package forge

// Topic wraps a single CommitLog (1 topic = 1 partition for monolith use).
type Topic struct {
	name string
	log  *CommitLog
}

// Name returns the topic name.
func (t *Topic) Name() string { return t.name }

// Log returns the underlying commit log.
func (t *Topic) Log() *CommitLog { return t.log }

// Close closes the topic's commit log.
func (t *Topic) Close() error { return t.log.Close() }
