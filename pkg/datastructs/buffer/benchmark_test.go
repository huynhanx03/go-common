package buffer

import (
	"testing"
)

// Pre-allocated data for benchmarks (avoid allocation in benchmark loop).
var (
	smallData  = make([]byte, 64)      // 64B - cache-friendly
	mediumData = make([]byte, 1024)    // 1KB - typical payload
	largeData  = make([]byte, 64*1024) // 64KB - bulk transfer
	stressData = make([]byte, 1<<20)   // 1MB - stress test
)

// sizes defines the benchmark size matrix.
var sizes = []struct {
	name string
	data []byte
}{
	{"64B", smallData},
	{"1KB", mediumData},
	{"64KB", largeData},
}

// =============================================================================
// BenchmarkWrite - Compare write performance across buffer implementations
// =============================================================================

func BenchmarkWrite(b *testing.B) {
	for _, size := range sizes {
		// Buffer (append-only)
		b.Run("Buffer/"+size.name, func(b *testing.B) {
			buf := New(len(size.data) * 2)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Write(size.data)
				buf.Reset()
			}
		})

		// RingBuffer (circular)
		b.Run("RingBuffer/"+size.name, func(b *testing.B) {
			buf := NewRing(len(size.data) * 2)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Write(size.data)
				buf.Reset()
			}
		})

		// LinkedListBuffer (linked nodes)
		b.Run("LinkedList/"+size.name, func(b *testing.B) {
			buf := &LinkedListBuffer{}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.PushBack(size.data)
				buf.Reset()
			}
		})

		// ElasticRing (pooled ring)
		b.Run("ElasticRing/"+size.name, func(b *testing.B) {
			var buf ElasticRing
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Write(size.data)
				buf.Reset()
			}
			buf.Done()
		})

		// ElasticBuffer (hybrid)
		b.Run("ElasticBuffer/"+size.name, func(b *testing.B) {
			buf, _ := NewElastic(len(size.data) * 2)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Write(size.data)
				buf.Reset(0)
			}
			buf.Release()
		})
	}
}

// =============================================================================
// BenchmarkRead - Compare read performance across buffer implementations
// =============================================================================

func BenchmarkRead(b *testing.B) {
	readBuf := make([]byte, 1024)

	// RingBuffer
	b.Run("RingBuffer/1KB", func(b *testing.B) {
		buf := NewRing(2048)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Read(readBuf)
		}
	})

	// LinkedListBuffer
	b.Run("LinkedList/1KB", func(b *testing.B) {
		buf := &LinkedListBuffer{}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.PushBack(mediumData)
			buf.Read(readBuf)
		}
	})

	// ElasticRing
	b.Run("ElasticRing/1KB", func(b *testing.B) {
		var buf ElasticRing
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Read(readBuf)
		}
		buf.Done()
	})

	// ElasticBuffer
	b.Run("ElasticBuffer/1KB", func(b *testing.B) {
		buf, _ := NewElastic(2048)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Read(readBuf)
		}
		buf.Release()
	})
}

// =============================================================================
// BenchmarkWriteThenRead - Full write-read cycle comparison
// =============================================================================

func BenchmarkWriteThenRead(b *testing.B) {
	data := mediumData
	readBuf := make([]byte, len(data))

	// RingBuffer - full cycle
	b.Run("RingBuffer", func(b *testing.B) {
		buf := NewRing(len(data) * 2)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(data)
			buf.Read(readBuf)
		}
	})

	// LinkedListBuffer - full cycle
	b.Run("LinkedList", func(b *testing.B) {
		buf := &LinkedListBuffer{}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.PushBack(data)
			buf.Read(readBuf)
		}
	})

	// ElasticRing - full cycle
	b.Run("ElasticRing", func(b *testing.B) {
		var buf ElasticRing
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(data)
			buf.Read(readBuf)
		}
		buf.Done()
	})

	// ElasticBuffer - full cycle
	b.Run("ElasticBuffer", func(b *testing.B) {
		buf, _ := NewElastic(len(data) * 2)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(data)
			buf.Read(readBuf)
		}
		buf.Release()
	})
}

// =============================================================================
// BenchmarkGrow - Memory growth patterns
// =============================================================================

func BenchmarkGrow(b *testing.B) {
	// Buffer growth pattern
	b.Run("Buffer/Incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf := New(64)
			for j := 0; j < 16; j++ {
				buf.Write(largeData) // Forces multiple grows
			}
		}
	})

	// RingBuffer growth pattern
	b.Run("RingBuffer/Incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf := NewRing(64)
			for j := 0; j < 16; j++ {
				buf.Write(largeData) // Forces multiple grows
			}
		}
	})

	// LinkedListBuffer (no grow, just appends nodes)
	b.Run("LinkedList/Incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf := &LinkedListBuffer{}
			for j := 0; j < 16; j++ {
				buf.PushBack(largeData)
			}
			buf.Reset()
		}
	})
}

// =============================================================================
// BenchmarkReset - Reuse efficiency
// =============================================================================

func BenchmarkReset(b *testing.B) {
	// Buffer reset
	b.Run("Buffer", func(b *testing.B) {
		buf := New(2048)
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Reset()
		}
	})

	// RingBuffer reset
	b.Run("RingBuffer", func(b *testing.B) {
		buf := NewRing(2048)
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Reset()
		}
	})

	// LinkedListBuffer reset
	b.Run("LinkedList", func(b *testing.B) {
		buf := &LinkedListBuffer{}
		buf.PushBack(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.PushBack(mediumData)
			buf.Reset()
		}
	})

	// ElasticRing reset
	b.Run("ElasticRing", func(b *testing.B) {
		var buf ElasticRing
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Reset()
		}
		buf.Done()
	})

	// ElasticBuffer reset
	b.Run("ElasticBuffer", func(b *testing.B) {
		buf, _ := NewElastic(2048)
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(mediumData)
			buf.Reset(0)
		}
		buf.Release()
	})
}

// =============================================================================
// BenchmarkPeek - Non-consuming read comparison
// =============================================================================

func BenchmarkPeek(b *testing.B) {
	// RingBuffer peek
	b.Run("RingBuffer", func(b *testing.B) {
		buf := NewRing(2048)
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Peek(512)
		}
	})

	// LinkedListBuffer peek
	b.Run("LinkedList", func(b *testing.B) {
		buf := &LinkedListBuffer{}
		buf.PushBack(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Peek(512)
		}
	})

	// ElasticRing peek
	b.Run("ElasticRing", func(b *testing.B) {
		var buf ElasticRing
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Peek(512)
		}
		buf.Done()
	})

	// ElasticBuffer peek
	b.Run("ElasticBuffer", func(b *testing.B) {
		buf, _ := NewElastic(2048)
		buf.Write(mediumData)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Peek(512)
		}
		buf.Release()
	})
}

// =============================================================================
// BenchmarkStress - Stress test with large data (1MB)
// =============================================================================

func BenchmarkStress(b *testing.B) {
	// Buffer with 1MB writes
	b.Run("Buffer/1MB", func(b *testing.B) {
		buf := New(1 << 21) // 2MB initial
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(stressData)
			buf.Reset()
		}
	})

	// RingBuffer with 1MB writes
	b.Run("RingBuffer/1MB", func(b *testing.B) {
		buf := NewRing(1 << 21) // 2MB initial
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.Write(stressData)
			buf.Reset()
		}
	})

	// LinkedListBuffer with 1MB writes
	b.Run("LinkedList/1MB", func(b *testing.B) {
		buf := &LinkedListBuffer{}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf.PushBack(stressData)
			buf.Reset()
		}
	})
}
