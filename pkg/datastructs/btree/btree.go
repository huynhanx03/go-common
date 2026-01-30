package btree

import (
	"math"

	"github.com/huynhanx03/go-common/pkg/datastructs/buffer"
	bufferpool "github.com/huynhanx03/go-common/pkg/pool/buffer"
	"github.com/huynhanx03/go-common/pkg/utils"
)

type Tree struct {
	buffer   *buffer.Buffer
	data     []byte
	nextPage uint64
	freePage uint64
	stats    TreeStats
}

func (t *Tree) initRootNode() {
	t.newNode(0)
	t.Set(absoluteMax, 0)
}

// NewTree returns an in-memory B+ tree.
func NewTree() *Tree {
	// Use pool for large 1MB buffer allocation
	buf := bufferpool.GetSize(minSize)

	// Set callback to return to pool on Release
	buf.ReleaseFn = func() {
		bufferpool.Put(buf)
	}

	t := &Tree{buffer: buf}
	t.Reset()
	return t
}

// Reset resets the tree and truncates it to minSize. It ensures the root node is re-initialized.
func (t *Tree) Reset() {
	t.buffer.Reset()
	t.buffer.AllocateOffset(minSize)
	t.data = t.buffer.Bytes()
	t.stats = TreeStats{}
	t.nextPage = 1
	t.freePage = 0
	t.initRootNode()
}

// Close releases the memory used by the tree.
func (t *Tree) Close() error {
	if t == nil {
		return nil
	}
	return t.buffer.Release()
}

type TreeStats struct {
	Allocated    int     // Derived.
	Bytes        int     // Derived.
	NumLeafKeys  int     // Calculated.
	NumPages     int     // Derived.
	NumPagesFree int     // Calculated.
	Occupancy    float64 // Derived.
	PageSize     int     // Derived.
}

// Stats returns stats about the tree.
func (t *Tree) Stats() TreeStats {
	numPages := int(t.nextPage - 1)
	out := TreeStats{
		Bytes:        numPages * pageSize,
		Allocated:    len(t.data),
		NumLeafKeys:  t.stats.NumLeafKeys,
		NumPages:     numPages,
		NumPagesFree: t.stats.NumPagesFree,
		PageSize:     pageSize,
	}
	out.Occupancy = 100.0 * float64(out.NumLeafKeys) / float64(maxKeys*numPages)
	return out
}

func (t *Tree) newNode(bit uint64) node {
	var pid uint64
	if t.freePage > 0 {
		pid = t.freePage
		t.stats.NumPagesFree--
	} else {
		pid = t.nextPage
		t.nextPage++
		offset := int(pid) * pageSize
		reqSize := offset + pageSize
		if reqSize > len(t.data) {
			t.buffer.AllocateOffset(reqSize - len(t.data))
			t.data = t.buffer.Bytes()
		}
	}
	n := t.node(pid)
	if t.freePage > 0 {
		t.freePage = n.uint64(0)
	}
	zeroOut(n)
	n.setBit(bit)
	n.setAt(metaPidIdx, pid)
	return n
}

func getNode(data []byte) node {
	return node(utils.BytesToUint64Slice(data))
}

func zeroOut(data []uint64) {
	for i := 0; i < len(data); i++ {
		data[i] = 0
	}
}

// node returns the node at the given page ID.
func (t *Tree) node(pid uint64) node {
	if pid == 0 {
		return nil
	}
	start := pageSize * int(pid)
	return getNode(t.data[start : start+pageSize])
}

// Set sets the key-value pair in the tree.
func (t *Tree) Set(k, v uint64) {
	if k == math.MaxUint64 || k == 0 {
		panic("Error setting zero or MaxUint64")
	}
	root := t.set(1, k, v)
	if root.isFull() {
		right := t.split(1)
		left := t.newNode(root.bits())
		root = t.node(1)
		copy(left[:keyOffset(maxKeys)], root)
		left.setNumKeys(root.numKeys())

		zeroOut(root)
		root.setNumKeys(0)

		root.set(left.maxKey(), left.pid())
		root.set(right.maxKey(), right.pid())
	}
}

// set recursively inserts the key-value pair and returns the node itself.
func (t *Tree) set(pid, k, v uint64) node {
	n := t.node(pid)
	if n.isLeaf() {
		t.stats.NumLeafKeys += n.set(k, v)
		return n
	}

	idx := n.search(k)
	if idx >= maxKeys {
		panic("search returned index >= maxKeys")
	}

	if n.key(idx) == 0 {
		n.setAt(keyOffset(idx), k)
		n.setNumKeys(n.numKeys() + 1)
	}
	child := t.node(n.val(idx))
	if child == nil {
		child = t.newNode(bitLeaf)
		n = t.node(pid)
		n.setAt(valOffset(idx), child.pid())
	}
	child = t.set(child.pid(), k, v)

	n = t.node(pid)
	if child.isFull() {
		nn := t.split(child.pid())

		n = t.node(pid)
		child = t.node(n.uint64(valOffset(idx)))

		n.set(child.maxKey(), child.pid())
		n.set(nn.maxKey(), nn.pid())
	}
	return n
}

// Get looks for key and returns the corresponding value.
// If key is not found, 0 is returned.
func (t *Tree) Get(k uint64) uint64 {
	if k == math.MaxUint64 || k == 0 {
		panic("Does not support getting MaxUint64/Zero")
	}
	root := t.node(1)
	return t.get(root, k)
}

func (t *Tree) get(n node, k uint64) uint64 {
	if n.isLeaf() {
		return n.get(k)
	}
	// This is internal node
	idx := n.search(k)
	if idx == n.numKeys() || n.key(idx) == 0 {
		return 0
	}
	child := t.node(n.uint64(valOffset(idx)))
	if child == nil {
		panic("child is nil")
	}
	return t.get(child, k)
}

func (t *Tree) iterate(n node, fn func(node)) {
	fn(n)
	if n.isLeaf() {
		return
	}
	// Explore children.
	for i := 0; i < maxKeys; i++ {
		if n.key(i) == 0 {
			return
		}
		childID := n.uint64(valOffset(i))
		child := t.node(childID)
		t.iterate(child, fn)
	}
}

// Iterate iterates over the tree and executes the fn on each node.
func (t *Tree) Iterate(fn func(node)) {
	root := t.node(1)
	t.iterate(root, fn)
}

// IterateKV iterates through all keys and values in the tree.
// If newVal is non-zero, it will be set in the tree.
func (t *Tree) IterateKV(f func(key, val uint64) (newVal uint64)) {
	t.Iterate(func(n node) {
		// Only leaf nodes contain keys.
		if !n.isLeaf() {
			return
		}

		for i := 0; i < n.numKeys(); i++ {
			key := n.key(i)
			val := n.val(i)

			// A zero value here means that this is a bogus entry.
			if val == 0 {
				continue
			}

			newVal := f(key, val)
			if newVal != 0 {
				n.setAt(valOffset(i), newVal)
			}
		}
	})
}

// split splits a full node into two, returning the new right sibling.
func (t *Tree) split(pid uint64) node {
	n := t.node(pid)
	if !n.isFull() {
		panic("split called on non-full node")
	}

	nn := t.newNode(n.bits())
	n = t.node(pid)

	copy(nn[keyOffset(0):], n[keyOffset(maxKeys/2):keyOffset(maxKeys)])
	copy(nn[valOffset(0):], n[valOffset(maxKeys/2):valOffset(maxKeys)])

	nn.setNumKeys(maxKeys - maxKeys/2)

	zeroOut(n[keyOffset(maxKeys/2):keyOffset(maxKeys)])
	zeroOut(n[valOffset(maxKeys/2):valOffset(maxKeys)])
	n.setNumKeys(maxKeys / 2)
	return nn
}

// DeleteBelow deletes all keys with value under ts.
func (t *Tree) DeleteBelow(ts uint64) {
	root := t.node(1)
	t.stats.NumLeafKeys = 0
	t.compact(root, ts)
	if root.numKeys() < 1 {
		// Root should have at least 1 key.
	}
}

// recursiveFree reclaims the subtree rooted at n, adding pages to the free list and updating stats.
func (t *Tree) recursiveFree(n node, pid uint64) {
	if n.isLeaf() {
		t.stats.NumLeafKeys -= n.numKeys()
		n.setAt(0, t.freePage)
		t.freePage = pid
		t.stats.NumPagesFree++
		return
	}
	// Internal node: Recurse on children.
	N := n.numKeys()
	for i := 0; i < N; i++ {
		childID := n.uint64(valOffset(i))
		child := t.node(childID)
		t.recursiveFree(child, childID)
	}
	// Free the node itself.
	n.setAt(0, t.freePage)
	t.freePage = pid
	t.stats.NumPagesFree++
}

// compact recursively removes keys with value < ts from the node and its children.
func (t *Tree) compact(n node, ts uint64) int {
	if n.isLeaf() {
		numKeys := n.compact(ts)
		t.stats.NumLeafKeys += n.numKeys()
		return numKeys
	}
	// Not leaf.
	N := n.numKeys()
	for i := 0; i < N; i++ {
		if n.key(i) == 0 {
			panic("key is zero")
		}
		// Optimization: If the max key of the child is < ts, the entire subtree is expired.
		// We can fast-path drop it without verifying every key.
		if n.key(i) < ts {
			childID := n.uint64(valOffset(i))
			child := t.node(childID)
			t.recursiveFree(child, childID) // Fast Drop

			// Remove entry from current node immediately
			n.setAt(valOffset(i), 0)
			continue
		}

		childID := n.uint64(valOffset(i))
		child := t.node(childID)
		if rem := t.compact(child, ts); rem == 0 && i < N-1 {
			// If no valid key is remaining we can drop this child. However, don't do that if this
			// is the max key.
			t.stats.NumLeafKeys -= child.numKeys()
			child.setAt(0, t.freePage)
			t.freePage = childID
			n.setAt(valOffset(i), 0)
			t.stats.NumPagesFree++
		}
	}
	// We use ts=1 here because we want to delete all the keys whose value is 0, which means they no
	// longer have a valid page for that key.
	return n.compact(1)
}
