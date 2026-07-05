package algorithm

import (
	"math"
	"math/rand/v2"
	"time"
)

// XFetchShouldRefresh implements the probabilistic early expiration decision
// from "Optimal Probabilistic Cache Stampede Prevention" (Vattani et al.,
// VLDB 2015), a.k.a. XFetch. It reports whether a cached value should be
// recomputed now, ahead of its expiry:
//
//	now − delta·β·ln(rand) ≥ expireAt
//
// delta is how long the value takes to recompute and beta tunes eagerness
// (1.0 is the paper's recommendation; >1 refreshes earlier, <1 later). The
// −ln(rand) draw makes each caller decide independently — the probability of
// an early refresh rises smoothly to 1 as expiry approaches, so refreshes
// spread out instead of stampeding, and slow-to-compute values start
// refreshing earlier.
func XFetchShouldRefresh(expireAt time.Time, delta time.Duration, beta float64) bool {
	u := 1 - rand.Float64() // (0,1] — avoids ln(0)
	spread := time.Duration(float64(delta) * beta * -math.Log(u))
	return !time.Now().Add(spread).Before(expireAt)
}
