package settings

import "crypto/rsa"

type JWT struct {
	Secret            string          `mapstructure:"secret"`
	Issuer            string          `mapstructure:"issuer"`
	Audience          string          `mapstructure:"audience"`
	KeyID             string          `mapstructure:"key_id"`
	AccessTTLSeconds  int             `mapstructure:"access_ttl_seconds"`
	RefreshTTLSeconds int             `mapstructure:"refresh_ttl_seconds"`
	MaxTTLSeconds     int             `mapstructure:"max_ttl_seconds"`
	LeewaySeconds     int             `mapstructure:"leeway_seconds"`
	MaxTokenBytes     int             `mapstructure:"max_token_bytes"`
	PrivateKeyPath    string          `mapstructure:"private_key_path"`
	PublicKeyPath     string          `mapstructure:"public_key_path"`
	PrivateKey        *rsa.PrivateKey `mapstructure:"-"`
	PublicKey         *rsa.PublicKey  `mapstructure:"-"`
}

type Password struct {
	MemoryKiB     uint32 `mapstructure:"memory_kib"`
	Iterations    uint32 `mapstructure:"iterations"`
	Parallelism   uint8  `mapstructure:"parallelism"`
	MaxConcurrent int    `mapstructure:"max_concurrent"`
}

// Casbin configures Casbin policy loading.
type Casbin struct {
	TableName   string `mapstructure:"table_name"`   // Empty uses the adapter default.
	PolicyLimit int    `mapstructure:"policy_limit"` // Zero uses the adapter default.
}
