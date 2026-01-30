package btree

// node represents a BTree node.
// Layout: [MetaPid | MetaInfo | Keys... | Vals...]
type node []uint64

// Helper Offset functions for SoA layout
// keyOffset returns the offset of the key at index i.
func keyOffset(i int) int { return metaOffset + i }

// valOffset returns the offset of the value at index i.
func valOffset(i int) int { return metaOffset + maxKeys + i }

func (n node) uint64(start int) uint64 { return n[start] }

// Metadata Accessors
func (n node) pid() uint64      { return n.uint64(metaPidIdx) }
func (n node) key(i int) uint64 { return n.uint64(keyOffset(i)) }
func (n node) val(i int) uint64 { return n.uint64(valOffset(i)) }

func (n node) setAt(start int, k uint64) {
	n[start] = k
}

// numKeys returns the number of keys. Stored in lower 32 bits of MetaInfo.
func (n node) numKeys() int {
	return int(n[metaInfoIdx] & maskNumKeys)
}

// setNumKeys sets the number of keys in the node, preserving flags.
func (n node) setNumKeys(num int) {
	n[metaInfoIdx] = (n[metaInfoIdx] & ^maskNumKeys) | uint64(num)
}

func (n node) moveRight(lo int) {
	hi := n.numKeys()
	copy(n[keyOffset(lo+1):keyOffset(hi+1)], n[keyOffset(lo):keyOffset(hi)])
	copy(n[valOffset(lo+1):valOffset(hi+1)], n[valOffset(lo):valOffset(hi)])
}

// setBit sets a specific bit flag in the node metadata.
func (n node) setBit(b uint64) {
	n[metaInfoIdx] |= b
}

func (n node) bits() uint64 {
	return n[metaInfoIdx] & maskBits
}

func (n node) isLeaf() bool {
	return n.bits()&bitLeaf > 0
}

func (n node) isFull() bool {
	return n.numKeys() == maxKeys
}

// search returns the index of a smallest key >= k in a node.
func (n node) search(k uint64) int {
	N := n.numKeys()
	if N < 4 {
		for i := 0; i < N; i++ {
			if ki := n.key(i); ki >= k {
				return i
			}
		}
		return N
	}

	// Binary search for larger nodes.
	lo, hi := 0, N
	for lo < hi {
		mid := (lo + hi) / 2
		if n.key(mid) < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (n node) maxKey() uint64 {
	idx := n.numKeys()
	if idx > 0 {
		idx--
	}
	return n.key(idx)
}

// compact removes all keys with value < lo and returns the remaining number of keys.
func (n node) compact(lo uint64) int {
	N := n.numKeys()
	mk := n.maxKey()
	var left, right int
	for right = 0; right < N; right++ {
		if n.val(right) < lo && n.key(right) < mk {
			// Skip over this key. Don't copy it.
			continue
		}
		// Valid data. Copy it from right to left. Advance left.
		if left != right {
			n.setAt(keyOffset(left), n.key(right))
			n.setAt(valOffset(left), n.val(right))
		}
		left++
	}
	// zero out rest of the kv pairs.
	zeroOut(n[keyOffset(left):keyOffset(right)])
	zeroOut(n[valOffset(left):valOffset(right)])
	n.setNumKeys(left)

	// If the only key we have is the max key, and its value is less than lo, then we can indicate
	// to the caller by returning a zero that it's OK to drop the node.
	if left == 1 && n.key(0) == mk && n.val(0) < lo {
		return 0
	}
	return left
}

func (n node) get(k uint64) uint64 {
	idx := n.search(k)
	if idx == n.numKeys() {
		return 0
	}
	if ki := n.key(idx); ki == k {
		return n.val(idx)
	}
	return 0
}

func (n node) set(k, v uint64) (numAdded int) {
	idx := n.search(k)
	ki := n.key(idx)
	if ki > k {
		n.moveRight(idx)
	}
	if ki != k {
		n.setNumKeys(n.numKeys() + 1)
		numAdded = 1
	}
	if ki == 0 || ki >= k {
		n.setAt(keyOffset(idx), k)
		n.setAt(valOffset(idx), v)
		return
	}
	panic("shouldn't reach here")
}
