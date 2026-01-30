package buffer

const (
	// defaultCapacity is the default initial capacity for a new Buffer.
	defaultCapacity = 64

	// headerSize is the number of bytes reserved for the length header of each block.
	headerSize = 8

	// sortChunkSize is the size of chunks used during SortSlice (merge sort).
	// We pick pivots every sortChunkSize items.
	sortChunkSize = 1024

	// maxGrowth is the maximum amount of bytes to grow by in a single step (1GB).
	maxGrowth = 1 << 30
)
