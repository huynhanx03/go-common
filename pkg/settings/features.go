package settings

// DefaultToggleRefreshIntervalSeconds is the SDK's refresh interval when unset.
const DefaultToggleRefreshIntervalSeconds = 15

// DefaultToggleSourceTimeoutSeconds bounds a source load when unset.
const DefaultToggleSourceTimeoutSeconds = 5

// Toggle configures local feature flag evaluation for one service scope.
type Toggle struct {
	Namespace              string `mapstructure:"namespace"`
	Environment            Env    `mapstructure:"environment"`
	AppName                string `mapstructure:"app_name"`
	RefreshIntervalSeconds int    `mapstructure:"refresh_interval_seconds"` // Zero uses DefaultToggleRefreshIntervalSeconds.
	SourceTimeoutSeconds   int    `mapstructure:"source_timeout_seconds"`   // Zero uses DefaultToggleSourceTimeoutSeconds.
}

// I18n configures one domain's lazy locale client. Zero seconds use package defaults.
type I18n struct {
	Domain               string `mapstructure:"domain"`
	CacheTTLSeconds      int    `mapstructure:"cache_ttl_seconds"`
	SourceTimeoutSeconds int    `mapstructure:"source_timeout_seconds"`
}
