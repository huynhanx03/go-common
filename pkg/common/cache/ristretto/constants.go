package ristretto

import "time"

const (
	defaultCost      int64 = 1
	versionTTL             = 24 * time.Hour
	fenceStripeCount       = 256
	dataKeyPrefix          = "data:"
	versionKeyPrefix       = "version:"
)

func versionKey(key string) string { return versionKeyPrefix + key }
