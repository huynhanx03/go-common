package sketch

type cmRow []byte

func newCmRow(numCounters int64) cmRow {
	return make(cmRow, numCounters/2)
}

func (r cmRow) get(n uint64) byte {
	return (r[n/2] >> ((n & 1) * counterShift)) & maxCount
}

func (r cmRow) increment(n uint64) {
	s := (n & 1) * counterShift
	i := n / 2
	v := (r[i] >> s) & maxCount
	if v < maxCount {
		r[i] += 1 << s
	}
}

func (r cmRow) reset() {
	for i := range r {
		r[i] = (r[i] >> 1) & agingMask
	}
}

func (r cmRow) clear() {
	for i := range r {
		r[i] = 0
	}
}
