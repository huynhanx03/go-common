// Package toggle provides service-owned feature flag contracts.
package toggle

import "github.com/huynhanx03/go-common/pkg/toggle/core"

type (
	Scope             = core.Scope
	Flag              = core.Flag
	Rule              = core.Rule
	Constraint        = core.Constraint
	Variant           = core.Variant
	Payload           = core.Payload
	Snapshot          = core.Snapshot
	EvaluationContext = core.EvaluationContext
	VariantResult     = core.VariantResult
)
