package ent

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/google/uuid"

	"github.com/huynhanx03/go-common/pkg/unique"
)

// UUIDMixin provides a UUIDv7 primary key. v7 IDs are time-ordered (the
// prefix encodes the creation time), so they insert append-only into B-tree
// indexes and sort chronologically — unlike random v4 IDs, which fragment
// the index.
type UUIDMixin struct {
	mixin.Schema
}

// Fields of the UUIDMixin.
func (UUIDMixin) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(NewUUID).
			Immutable(),
	}
}

// NewUUID returns a time-ordered UUID (v7). NewV7's only error source is
// crypto/rand, which since Go 1.24 cannot fail (the runtime aborts instead),
// so its error path is dead code and Must is safe.
func NewUUID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// PublicIDMixin adds a short human-friendly public_id column — the ID shown
// to users while the UUID primary key stays internal. One line per entity,
// with its own prefix:
//
//	func (User) Mixin() []ent.Mixin {
//		return []ent.Mixin{
//			e.UUIDMixin{},
//			e.PublicIDMixin{Prefix: "UR"}, // "UR20260705xK9"
//		}
//	}
//
// RandLen defaults to 3 (62³ ≈ 238k IDs per prefix per day). The column is
// unique — on the rare collision the insert fails with a constraint error,
// so retry the create once where volume makes that plausible, or raise
// RandLen.
type PublicIDMixin struct {
	mixin.Schema
	Prefix  string
	RandLen int
}

// Fields of the PublicIDMixin.
func (m PublicIDMixin) Fields() []ent.Field {
	n := m.RandLen
	if n <= 0 {
		n = 3
	}
	return []ent.Field{
		field.String("public_id").
			Unique().
			Immutable().
			DefaultFunc(func() string { return unique.PublicID(m.Prefix, time.Now(), n) }),
	}
}
