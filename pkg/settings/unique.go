package settings

type Snowflake struct {
	Epoch     int64 `mapstructure:"epoch"`
	Node      uint8 `mapstructure:"node"`
	Step      uint8 `mapstructure:"step"`
	TotalBits uint8 `mapstructure:"total_bits"`
}

type SnowflakeNode struct {
	Config   Snowflake
	WorkerID int64 `mapstructure:"worker_id"`
}
