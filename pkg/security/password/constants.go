package password

const (
	defaultMemoryKiB        uint32 = 64 * 1024
	defaultIterations       uint32 = 3
	defaultParallelism      uint8  = 2
	defaultSaltBytes        uint32 = 16
	defaultKeyBytes         uint32 = 32
	defaultMaxPasswordBytes        = 1024
	defaultMaxPHCBytes             = 512
	defaultMaxConcurrent           = 2

	hardMaxMemoryKiB     = uint32(1024 * 1024)
	hardMaxIterations    = uint32(10)
	hardMaxParallelism   = uint8(16)
	hardMaxSaltBytes     = uint32(64)
	hardMaxKeyBytes      = uint32(64)
	hardMaxPasswordBytes = 1 << 20
	hardMaxPHCBytes      = 4 << 10
	hardMaxConcurrent    = 1024

	argon2Version          = 19
	argon2VersionPHC       = "v=19"
	minBcryptCost          = 10
	maxBcryptCost          = 14
	maxBcryptPasswordBytes = 72
	argon2idPHCPrefix      = "$argon2id$"
	argon2idAlgorithm      = "argon2id"
	bcryptAlgorithm        = "bcrypt"
)
