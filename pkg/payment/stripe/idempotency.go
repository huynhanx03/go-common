package stripe

import stripesdk "github.com/stripe/stripe-go/v85"

// setIdempotency forwards a caller's idempotency key to Stripe so an identical
// retry is de-duplicated. An empty key is left unset.
func setIdempotency(params *stripesdk.Params, key string) {
	if key == "" {
		return
	}

	params.IdempotencyKey = stripesdk.String(key)
}

// scopeKey namespaces one intent's key across the several writes it may need,
// because Stripe rejects a key replayed with different parameters.
func scopeKey(key, scope string) string {
	if key == "" {
		return ""
	}

	return key + ":" + scope
}
