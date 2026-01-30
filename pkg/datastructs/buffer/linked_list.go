package buffer

import (
	"io"
	"math"

	"github.com/huynhanx03/go-common/pkg/pool/byteslice"
)

const minReadChunkSize = 512

// node represents a single node in the linked list buffer.
type node struct {
	data []byte
	next *node
}

// length returns the byte length of this node's data.
func (n *node) length() int {
	return len(n.data)
}

// LinkedListBuffer is a linked list of byte slices with pool integration.
// It provides efficient append/pop operations and implements io.ReadWriter.
type LinkedListBuffer struct {
	head      *node
	tail      *node
	nodeCount int
	byteCount int
}

// Read implements io.Reader.
// Reads data from the buffer into p, removing consumed nodes.
func (ll *LinkedListBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	var totalRead int
	for n := ll.popFront(); n != nil; n = ll.popFront() {
		copied := copy(p[totalRead:], n.data)
		totalRead += copied

		// Partial read: push remaining data back to front
		if copied < n.length() {
			n.data = n.data[copied:]
			ll.pushFront(n)
		} else {
			byteslice.Put(n.data)
		}

		if totalRead == len(p) {
			return totalRead, nil
		}
	}

	if totalRead == 0 {
		return 0, io.EOF
	}
	return totalRead, nil
}

// AllocNode allocates a []byte from the pool.
func (ll *LinkedListBuffer) AllocNode(size int) []byte {
	return byteslice.Get(size)
}

// FreeNode returns a []byte to the pool.
func (ll *LinkedListBuffer) FreeNode(p []byte) {
	byteslice.Put(p)
}

// Append adds p to the tail without copying (zero-copy).
// Caller must ensure p is allocated from the pool or will not be modified.
func (ll *LinkedListBuffer) Append(p []byte) {
	if len(p) == 0 {
		return
	}
	ll.pushBack(&node{data: p})
}

// Pop removes and returns the head buffer.
// Caller is responsible for returning the buffer to the pool.
func (ll *LinkedListBuffer) Pop() []byte {
	n := ll.popFront()
	if n == nil {
		return nil
	}
	return n.data
}

// PushFront copies p and adds it to the head.
func (ll *LinkedListBuffer) PushFront(p []byte) {
	dataLen := len(p)
	if dataLen == 0 {
		return
	}

	buf := byteslice.Get(dataLen)
	copy(buf, p)
	ll.pushFront(&node{data: buf})
}

// PushBack copies p and adds it to the tail.
func (ll *LinkedListBuffer) PushBack(p []byte) {
	dataLen := len(p)
	if dataLen == 0 {
		return
	}

	buf := byteslice.Get(dataLen)
	copy(buf, p)
	ll.pushBack(&node{data: buf})
}

// Peek returns up to maxBytes as [][]byte without advancing the read position.
// If maxBytes <= 0, returns all buffered data.
func (ll *LinkedListBuffer) Peek(maxBytes int) ([][]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt32 {
		maxBytes = math.MaxInt32
	} else if maxBytes > ll.Buffered() {
		return nil, io.ErrShortBuffer
	}

	return ll.collectBytes(maxBytes, nil), nil
}

// PeekWithBytes is like Peek but prepends existing slices to the result.
func (ll *LinkedListBuffer) PeekWithBytes(maxBytes int, existing ...[]byte) ([][]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt32 {
		maxBytes = math.MaxInt32
	} else {
		var existingLen int
		for _, b := range existing {
			existingLen += len(b)
		}
		if maxBytes > ll.Buffered()+existingLen {
			return nil, io.ErrShortBuffer
		}
	}

	return ll.collectBytes(maxBytes, existing), nil
}

// collectBytes gathers byte slices up to maxBytes, optionally prepending existing slices.
func (ll *LinkedListBuffer) collectBytes(maxBytes int, existing [][]byte) [][]byte {
	var result [][]byte
	var collected int

	// Process existing slices first
	for _, b := range existing {
		sliceLen := len(b)
		if sliceLen == 0 {
			continue
		}

		toTake := sliceLen
		if collected+toTake > maxBytes {
			toTake = maxBytes - collected
		}

		result = append(result, b[:toTake])
		collected += toTake

		if collected == maxBytes {
			return result
		}
	}

	// Collect from linked list nodes
	for current := ll.head; current != nil; current = current.next {
		nodeLen := current.length()
		toTake := nodeLen
		if collected+toTake > maxBytes {
			toTake = maxBytes - collected
		}

		result = append(result, current.data[:toTake])
		collected += toTake

		if collected == maxBytes {
			break
		}
	}

	return result
}

// Discard skips n bytes from the buffer.
// Returns the number of bytes actually discarded.
func (ll *LinkedListBuffer) Discard(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}

	var discarded int
	remaining := n

	for remaining > 0 {
		current := ll.popFront()
		if current == nil {
			break
		}

		nodeLen := current.length()
		if remaining < nodeLen {
			// Partial discard: push remaining data back
			current.data = current.data[remaining:]
			discarded += remaining
			ll.pushFront(current)
			break
		}

		// Full discard of this node
		remaining -= nodeLen
		discarded += nodeLen
		byteslice.Put(current.data)
	}

	return discarded, nil
}

// ReadFrom implements io.ReaderFrom.
// Reads data from r until EOF and appends it to the buffer.
func (ll *LinkedListBuffer) ReadFrom(r io.Reader) (int64, error) {
	var total int64

	for {
		buf := byteslice.Get(minReadChunkSize)
		bytesRead, err := r.Read(buf)
		if bytesRead < 0 {
			panic("linkedlist: reader returned negative count")
		}

		total += int64(bytesRead)
		buf = buf[:bytesRead]

		if err == io.EOF {
			byteslice.Put(buf)
			return total, nil
		}
		if err != nil {
			byteslice.Put(buf)
			return total, err
		}

		ll.pushBack(&node{data: buf})
	}
}

// WriteTo implements io.WriterTo.
// Writes all buffered data to w and frees the consumed nodes.
func (ll *LinkedListBuffer) WriteTo(w io.Writer) (int64, error) {
	var total int64

	for current := ll.popFront(); current != nil; current = ll.popFront() {
		written, err := w.Write(current.data)
		total += int64(written)

		if err != nil {
			return total, err
		}

		// Partial write: push remaining data back
		if written < current.length() {
			current.data = current.data[written:]
			ll.pushFront(current)
			return total, io.ErrShortWrite
		}

		byteslice.Put(current.data)
	}

	return total, nil
}

// Len returns the number of nodes in the buffer.
func (ll *LinkedListBuffer) Len() int {
	return ll.nodeCount
}

// Buffered returns the total number of bytes available to read.
func (ll *LinkedListBuffer) Buffered() int {
	return ll.byteCount
}

// IsEmpty returns true if the buffer contains no data.
func (ll *LinkedListBuffer) IsEmpty() bool {
	return ll.head == nil
}

// Reset clears the buffer and returns all memory to the pool.
func (ll *LinkedListBuffer) Reset() {
	for current := ll.popFront(); current != nil; current = ll.popFront() {
		byteslice.Put(current.data)
	}
	ll.head = nil
	ll.tail = nil
	ll.nodeCount = 0
	ll.byteCount = 0
}

// popFront removes and returns the head node.
func (ll *LinkedListBuffer) popFront() *node {
	if ll.head == nil {
		return nil
	}

	front := ll.head
	ll.head = front.next
	if ll.head == nil {
		ll.tail = nil
	}

	front.next = nil
	ll.nodeCount--
	ll.byteCount -= front.length()

	return front
}

// pushFront adds a node to the head of the list.
func (ll *LinkedListBuffer) pushFront(n *node) {
	if n == nil {
		return
	}

	if ll.head == nil {
		n.next = nil
		ll.tail = n
	} else {
		n.next = ll.head
	}

	ll.head = n
	ll.nodeCount++
	ll.byteCount += n.length()
}

// pushBack adds a node to the tail of the list.
func (ll *LinkedListBuffer) pushBack(n *node) {
	if n == nil {
		return
	}

	if ll.tail == nil {
		ll.head = n
	} else {
		ll.tail.next = n
	}

	n.next = nil
	ll.tail = n
	ll.nodeCount++
	ll.byteCount += n.length()
}
