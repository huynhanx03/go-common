package unleash

import (
	"time"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	defaultRefreshInterval = settings.DefaultToggleRefreshIntervalSeconds * time.Second
	defaultSourceTimeout   = settings.DefaultToggleSourceTimeoutSeconds * time.Second

	sdkBaseURL           = "http://toggle.invalid/api/"
	sdkHost              = "toggle.invalid"
	sdkFeaturesPath      = "/api/client/features"
	sdkInstanceID        = "toggle-local"
	sdkContentType       = "application/json"
	sdkRolloutStrategy   = "flexibleRollout"
	sdkVariantWeightType = "fix"
)
