package consumer

import (
	"context"
	"time"
)

// Collect waits for one source item, then gathers a bounded window. A zero
// wait drains only immediately available items. Closing the source flushes a
// partial window; cancellation leaves the window unprocessed.
func Collect[T any](ctx context.Context, source <-chan T, size int, wait time.Duration) ([]T, error) {
	if size <= 0 || wait < 0 {
		return nil, ErrInvalidConfig
	}
	var first T
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value, ok := <-source:
		if !ok {
			return nil, nil
		}
		first = value
	}
	batch := make([]T, 1, size)
	batch[0] = first
	var timer *time.Timer
	var timeout <-chan time.Time
	if wait > 0 {
		timer = time.NewTimer(wait)
		timeout = timer.C
		defer timer.Stop()
	}
	for len(batch) < size {
		if wait == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case value, ok := <-source:
				if !ok {
					return batch, nil
				}
				batch = append(batch, value)
			default:
				return batch, nil
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return batch, nil
		case value, ok := <-source:
			if !ok {
				return batch, nil
			}
			batch = append(batch, value)
		}
	}
	return batch, nil
}
