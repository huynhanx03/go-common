package settings

// Backoff configures an exponential delay sequence.
type Backoff struct {
	InitialDelayMilliseconds int     `mapstructure:"initial_delay_milliseconds"`
	MaxDelayMilliseconds     int     `mapstructure:"max_delay_milliseconds"`
	Multiplier               float64 `mapstructure:"multiplier"`
}
