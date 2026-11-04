package core

// Validation and resource limits shared by toggle implementations.
const (
	MaxIdentifierLength      = 100
	MaxPropertyNameLength    = 1024
	MaxFlagDescriptionLength = 4096
	MaxRulesPerFlag          = 32
	MaxConstraintsPerRule    = 32
	MaxVariantsPerRule       = 32
	MaxValuesPerConstraint   = 1000
	MaxConstraintValueLength = 1024
	MaxContextValueLength    = 4096
	MaxEvaluationProperties  = 100
	MaxRolloutPercent        = 100
	VariantWeightScale       = 1000
	MaxDefinitionSize        = 64 << 10
	MaxFlagsPerSnapshot      = 10_000
	MaxSnapshotSize          = 16 << 20
)
