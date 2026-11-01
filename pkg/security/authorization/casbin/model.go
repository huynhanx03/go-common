package casbin

import "github.com/huynhanx03/go-common/pkg/security/authorization"

// PolicyRule is one policy or grouping row supplied to a snapshot.
type PolicyRule struct {
	Type string
	V0   string
	V1   string
	V2   string
	V3   string
	V4   string
	V5   string
}

func (r PolicyRule) values() [6]string {
	return [6]string{r.V0, r.V1, r.V2, r.V3, r.V4, r.V5}
}

// Snapshot is the complete, revisioned Casbin model and policy set for a scope.
type Snapshot struct {
	Revision int64
	Model    string
	Rules    []PolicyRule
}

const defaultModel = `[request_definition]
r = sub, dom, obj, objID, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

g2 = _, _, _

g3 = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = (g(r.sub, p.sub, r.dom) || g(r.sub, p.sub, "system:default") || g2(r.sub, p.sub, r.objID)) && (p.dom == "*" || scopeMatch(r.dom, p.dom)) && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
`

// DefaultModel returns the shared scoped RBAC/ReBAC Casbin model.
// Policy rows use p = role, domain, resource, action; g = subject, role,
// domain; g2 = subject, role, resource; and g3 = child-domain, parent-domain.
func DefaultModel() string { return defaultModel }

// SharedModel returns DefaultModel.
//
// Deprecated: use DefaultModel.
func SharedModel() string { return DefaultModel() }

// SystemScope returns the default scope used by single-scope services.
//
// Deprecated: use authorization.DefaultSystemScope.
func SystemScope() authorization.Scope { return authorization.DefaultSystemScope() }
