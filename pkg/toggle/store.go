package toggle

import "github.com/huynhanx03/go-common/pkg/toggle/core"

// Store persists complete definitions using scope-level revision preconditions.
// Implementations must validate direct calls and return owned snapshots.
type Store = core.Store
