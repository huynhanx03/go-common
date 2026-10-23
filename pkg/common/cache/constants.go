package cache

import "time"

const (
	// MaxKeyBytes is the maximum accepted cache-key size in bytes.
	MaxKeyBytes            = 1024
	xfetchBeta             = 1.0
	maxConcurrentRefreshes = 128
	cacheTTLJitterFraction = 0.1
	NegativeKeySuffix      = ":neg" // NegativeKeySuffix is appended to a key for its negative-cache entry.
	NegativeTTL            = 30 * time.Second
	refreshKeySuffix       = ":refresh"

	operationL1DeleteMissing   = "l1_delete_missing"
	operationL1Promote         = "l1_promote"
	operationL1PromoteNegative = "l1_promote_negative"
	operationL1Set             = "l1_set"
	operationL1SetNegative     = "l1_set_negative"
	operationL1SnapshotVersion = "l1_snapshot_version"
	operationL2Decode          = "l2_decode"
	operationL2DecodeNegative  = "l2_decode_negative"
	operationL2Encode          = "l2_encode"
	operationL2EncodeNegative  = "l2_encode_negative"
	operationL2Get             = "l2_get"
	operationL2GetNegative     = "l2_get_negative"
	operationL2DeleteMissing   = "l2_delete_missing"
	operationL2Set             = "l2_set"
	operationL2SetNegative     = "l2_set_negative"
	operationL2SnapshotVersion = "l2_snapshot_version"
)
