package unique

import "time"

// PublicID builds a human-friendly public identifier: prefix + UTC date + n
// random base62 characters.
//
//	unique.PublicID("UR", time.Now(), 3) // "UR20260705xK9"
//
// Use it for IDs shown to users — profile codes, order numbers — keeping the
// internal UUID primary key private. Pick one prefix per entity ("UR" user,
// "OR" order, ...). n characters give 62^n combinations per prefix per day
// (n=3 ≈ 238k) — always pair the column with a unique constraint and retry
// on conflict, or raise n where volume demands it.
func PublicID(prefix string, t time.Time, n int) string {
	return prefix + t.UTC().Format("20060102") + RandBase62(n)
}
