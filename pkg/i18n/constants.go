package i18n

import "time"

const (
	DefaultCacheTTLSeconds      = 300
	DefaultSourceTimeoutSeconds = 5
	maxLoadAttempts             = 3
	maxInfoBytes                = 8 << 10
	maxLocaleTagBytes           = 64
	manifestKeyPrefix           = "i18n:manifest:"
	localeKeyPrefix             = "i18n:locale:"
	bundleKeyPrefix             = "i18n:bundle:"
	cacheInvalidationTimeout    = 5 * time.Second
	strictTemplateOption        = "missingkey=error"
)

const (
	MaxIdentifierBytes  = 128
	MaxKeys             = 5000
	MaxLocales          = 32
	MaxCatalogBytes     = 16 << 20
	MaxTemplateBytes    = 8 << 10
	MaxDescriptionBytes = 4096
	MaxParams           = 64
	MaxParamBytes       = 8192
	MaxLocaleBytes      = 512
	MaxPageSize         = 200
	MaxDomains          = 1000
)
