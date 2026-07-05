package forge

import "time"

// startRetentionLoop spawns a goroutine that periodically runs EnforceRetention
// on all topics. Stopped when stopCh is closed (via Broker.Close).
func (b *Broker) startRetentionLoop() {
	b.wg.Add(1)
	go b.retentionLoop()
}

func (b *Broker) retentionLoop() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.config.RetentionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.runRetention()
		}
	}
}

// runRetention collects topic snapshots under RLock, then runs retention/merge outside the lock.
func (b *Broker) runRetention() {
	b.mu.RLock()
	topics := make([]*Topic, 0, len(b.topics))
	for _, t := range b.topics {
		topics = append(topics, t)
	}
	b.mu.RUnlock()

	for _, t := range topics {
		if err := t.log.EnforceRetention(); err != nil {
			b.reportRetentionError(err)
		}
		if err := t.log.MergeSegments(); err != nil {
			b.reportRetentionError(err)
		}
	}
}

// reportRetentionError routes retention/merge errors to the configured callback.
func (b *Broker) reportRetentionError(err error) {
	if b.config.OnRetentionError != nil {
		b.config.OnRetentionError(err)
	}
}
