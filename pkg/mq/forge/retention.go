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

// runRetention collects topic snapshots under RLock, then runs whole-segment
// retention outside the lock.
func (b *Broker) runRetention() {
	b.mu.RLock()
	topics := make([]*Topic, 0, len(b.topics))
	for _, t := range b.topics {
		topics = append(topics, t)
	}
	b.mu.RUnlock()

	for _, t := range topics {
		var retentionErr error
		if b.config.RetentionMode == RetainByAgeAndSize {
			retentionErr = t.log.EnforceRetention()
		} else {
			b.groupMu.Lock()
			minimum, registered, err := b.offsetStore.MinimumOffset(t.name)
			if err != nil {
				retentionErr = err
			} else if registered {
				retentionErr = t.log.EnforceRetentionBefore(minimum)
			}
			b.groupMu.Unlock()
		}
		if retentionErr != nil {
			b.reportRetentionError(retentionErr)
		}
	}
}

// reportRetentionError routes retention errors to the configured callback.
func (b *Broker) reportRetentionError(err error) {
	if b.config.OnRetentionError != nil {
		defer func() {
			_ = recover()
		}()
		b.config.OnRetentionError(err)
	}
}
