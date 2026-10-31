// Package toggle provides service-owned feature flag contracts.
package toggle

type Scope struct {
	Namespace   string `json:"namespace"`
	Environment string `json:"environment"`
}
type Flag struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Rules       []Rule `json:"rules"`
}
type Rule struct {
	Rollout     int          `json:"rollout"`
	GroupID     string       `json:"group_id"`
	Stickiness  string       `json:"stickiness"`
	Constraints []Constraint `json:"constraints"`
	Variants    []Variant    `json:"variants"`
}
type Constraint struct {
	ContextName     string   `json:"context_name"`
	Operator        string   `json:"operator"`
	Value           string   `json:"value"`
	Values          []string `json:"values"`
	CaseInsensitive bool     `json:"case_insensitive"`
	Inverted        bool     `json:"inverted"`
}
type Variant struct {
	Name    string   `json:"name"`
	Weight  int      `json:"weight"`
	Payload *Payload `json:"payload,omitempty"`
}
type Payload struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type Snapshot struct {
	Scope    Scope  `json:"scope"`
	Revision int64  `json:"revision"`
	Flags    []Flag `json:"flags"`
}
type EvaluationContext struct {
	UserID        string            `json:"user_id"`
	SessionID     string            `json:"session_id"`
	RemoteAddress string            `json:"remote_address"`
	Properties    map[string]string `json:"properties"`
}
type VariantResult struct {
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FeatureEnabled bool     `json:"feature_enabled"`
	Payload        *Payload `json:"payload,omitempty"`
}
