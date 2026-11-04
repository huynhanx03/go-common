package i18n

import "context"

func (m *Manager) invalidate(ctx context.Context, domain string, revision int64) error {
	// A disconnected HTTP caller must not skip invalidation after a committed write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheInvalidationTimeout)
	defer cancel()
	if err := m.cache.Delete(ctx, manifestKeyPrefix+domain); err != nil {
		return &CommittedError{Revision: revision, cause: err}
	}
	return nil
}

// Invalidate retries cache invalidation after a committed write
// returned CommittedError. It remains safe if a newer revision already exists.
func (m *Manager) Invalidate(ctx context.Context, domain string, revision int64) error {
	if !m.valid() {
		return ErrInvalidConfig
	}
	if err := contextOK(ctx); err != nil {
		return err
	}
	if ValidateDomain(domain) != nil || revision <= 0 {
		return ErrInvalidParams
	}
	return m.invalidate(ctx, domain, revision)
}
