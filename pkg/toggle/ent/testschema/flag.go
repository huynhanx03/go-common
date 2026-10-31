//go:build integration

package testschema

import (
	entgo "entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

type Flag struct{ entgo.Schema }

func (Flag) Mixin() []entgo.Mixin { return []entgo.Mixin{toggleent.FlagMixin{}} }
func (Flag) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "toggle_flags"}}
}
