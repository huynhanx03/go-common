package casbin

import (
	"sync/atomic"

	casbinlib "github.com/casbin/casbin/v3"
)

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

type Snapshot struct {
	Revision int64
	Model    string
	Rules    []PolicyRule
}

type liveSnapshot struct {
	revision int64
	enforcer *casbinlib.Enforcer
}

type runtimeNonce struct {
	marker byte
}

type PreparedSnapshot struct {
	owner    *runtimeNonce
	snapshot *liveSnapshot
	used     atomic.Bool
}
