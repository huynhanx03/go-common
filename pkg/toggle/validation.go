package toggle

import "github.com/huynhanx03/go-common/pkg/toggle/core"

// Identifier checks an identifier against a byte limit and error category.
func Identifier(value string, limit int, category error) error {
	return core.Identifier(value, limit, category)
}

// ValidateScope checks the explicitly supplied namespace and environment.
func ValidateScope(scope Scope) error { return core.ValidateScope(scope) }

// NormalizeFlag validates a definition and returns a deep owned copy.
func NormalizeFlag(flag Flag) (Flag, error) { return core.NormalizeFlag(flag) }

// NormalizeSnapshot validates a complete scoped candidate before publication.
func NormalizeSnapshot(scope Scope, snapshot Snapshot) (Snapshot, error) {
	return core.NormalizeSnapshot(scope, snapshot)
}
