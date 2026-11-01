package casbin

const (
	// DefaultMaxRoleHierarchyDepth matches Casbin's default role-link depth.
	DefaultMaxRoleHierarchyDepth = 10
	// DefaultMaxScopeHierarchyDepth bounds scope-ancestry traversal.
	DefaultMaxScopeHierarchyDepth = 16
	// SharedModelVersion identifies the canonical policy-row semantics.
	SharedModelVersion = "scoped-rbac-rebac-v1"

	defaultMaxModelBytes = 128 << 10
	defaultMaxRules      = 100_000
	defaultMaxValueBytes = 512
	maxModelBytes        = 1 << 20
	maxRules             = 1_000_000
	maxValueBytes        = 4096
	maxPolicyTypeBytes   = 64

	policyTypePolicy    = "p"
	policyTypeGrouping  = "g"
	policyTypeResource  = "g2"
	policyTypeScope     = "g3"
	requestModelSection = "r"
	policyModelSection  = "p"
	scopeMatcherName    = "scopeHierarchy"
	scopeFunctionName   = "scopeMatch"

	// NUL cannot appear in a validated policy value, so synthetic request
	// subjects cannot collide with persisted policy relations.
	dynamicSubjectPrefix   = "\x00authorization-subject:"
	scopedRuntimeKeyPrefix = "casbin:runtime:"
)
