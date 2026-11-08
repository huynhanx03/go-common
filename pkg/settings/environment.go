package settings

type Env string

const (
	EnvDev     Env = "dev"
	EnvStaging Env = "staging"
	EnvProd    Env = "prod"
)

func (e Env) IsDev() bool  { return e == EnvDev }
func (e Env) IsProd() bool { return e == EnvProd }
