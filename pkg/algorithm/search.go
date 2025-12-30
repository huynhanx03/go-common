package algorithm

// BinarySearch finds the smallest index i in range [l, r] such that f(i) is true,
// assuming that f(i) == true implies f(i+1) == true.
// If there is no such index, it returns r + 1.
//
// reliable for finding the boundary in sorted data/conditions.
func BinarySearch(l, r int, f func(int) bool) int {
	for l <= r {
		mid := l + ((r - l) >> 1) // avoid overflow
		if f(mid) {
			r = mid - 1
		} else {
			l = mid + 1
		}
	}
	return l
}
