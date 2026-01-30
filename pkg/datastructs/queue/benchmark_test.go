package queue

import (
	"sync"
	"testing"
)

// ===========================================================================
// Benchmark Configuration
// ===========================================================================

// queueBenchConfig holds benchmark test configuration.
type queueBenchConfig struct {
	name     string
	capacity int
}

// benchConfigs defines the data sizes for benchmarking.
// Add more configurations as needed for comparison.
var benchConfigs = []queueBenchConfig{
	{"Small/Cap64", 64},
	{"Medium/Cap1K", 1024},
	{"Large/Cap64K", 64 * 1024},
}

// ===========================================================================
// Queue Factory Registry
// ===========================================================================

// queueFactory creates a Queue[int] with the given capacity.
type queueFactory func(capacity int) Queue[int]

// queueImplementations holds all registered queue implementations.
// Add new implementations here when they are created.
var queueImplementations = map[string]queueFactory{
	"MPMC": func(capacity int) Queue[int] { return NewMPMC[int](capacity) },
	// Add more implementations here:
	// "SPSC": func(capacity int) Queue[int] { return NewSPSC[int](capacity) },
	// "Chan": func(capacity int) Queue[int] { return NewChanQueue[int](capacity) },
}

// ===========================================================================
// Single-Threaded Benchmarks
// ===========================================================================

// BenchmarkEnqueue measures Enqueue performance.
func BenchmarkEnqueue(b *testing.B) {
	for implName, factory := range queueImplementations {
		for _, cfg := range benchConfigs {
			name := implName + "/" + cfg.name
			b.Run(name, func(b *testing.B) {
				q := factory(cfg.capacity)
				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					q.Enqueue(i)
					// Drain to avoid full queue
					if i%cfg.capacity == cfg.capacity-1 {
						b.StopTimer()
						for j := 0; j < cfg.capacity; j++ {
							q.Dequeue()
						}
						b.StartTimer()
					}
				}
			})
		}
	}
}

// BenchmarkDequeue measures Dequeue performance.
func BenchmarkDequeue(b *testing.B) {
	for implName, factory := range queueImplementations {
		for _, cfg := range benchConfigs {
			name := implName + "/" + cfg.name
			b.Run(name, func(b *testing.B) {
				q := factory(cfg.capacity)
				// Pre-fill
				for i := 0; i < cfg.capacity; i++ {
					q.Enqueue(i)
				}

				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, ok := q.Dequeue()
					// Refill when empty
					if !ok {
						b.StopTimer()
						for j := 0; j < cfg.capacity; j++ {
							q.Enqueue(j)
						}
						b.StartTimer()
					}
				}
			})
		}
	}
}

// BenchmarkEnqueueDequeue measures roundtrip Enqueue+Dequeue.
func BenchmarkEnqueueDequeue(b *testing.B) {
	for implName, factory := range queueImplementations {
		for _, cfg := range benchConfigs {
			name := implName + "/" + cfg.name
			b.Run(name, func(b *testing.B) {
				q := factory(cfg.capacity)
				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					q.Enqueue(i)
					q.Dequeue()
				}
			})
		}
	}
}

// ===========================================================================
// Concurrent Benchmarks (MPMC specific)
// ===========================================================================

// concurrencyConfigs defines producer/consumer count combinations.
var concurrencyConfigs = []struct {
	name      string
	producers int
	consumers int
}{
	{"1P1C", 1, 1},
	{"2P2C", 2, 2},
	{"4P4C", 4, 4},
	{"8P8C", 8, 8},
}

// BenchmarkConcurrent_Enqueue measures concurrent Enqueue throughput.
func BenchmarkConcurrent_Enqueue(b *testing.B) {
	const capacity = 1024
	itemsPerProducer := 10000

	for implName, factory := range queueImplementations {
		for _, cc := range concurrencyConfigs {
			name := implName + "/" + cc.name
			b.Run(name, func(b *testing.B) {
				for n := 0; n < b.N; n++ {
					q := factory(capacity)
					var wg sync.WaitGroup

					// Consumer goroutine to drain queue
					done := make(chan struct{})
					go func() {
						for {
							select {
							case <-done:
								return
							default:
								q.Dequeue()
							}
						}
					}()

					b.ResetTimer()

					// Producers
					wg.Add(cc.producers)
					for p := 0; p < cc.producers; p++ {
						go func(id int) {
							defer wg.Done()
							for i := 0; i < itemsPerProducer; i++ {
								for !q.Enqueue(id*itemsPerProducer + i) {
									// Spin until enqueue succeeds
								}
							}
						}(p)
					}

					wg.Wait()
					close(done)
				}
			})
		}
	}
}

// BenchmarkConcurrent_EnqueueDequeue measures concurrent throughput.
func BenchmarkConcurrent_EnqueueDequeue(b *testing.B) {
	const capacity = 1024
	const opsPerGoroutine = 10000

	for implName, factory := range queueImplementations {
		for _, cc := range concurrencyConfigs {
			name := implName + "/" + cc.name
			b.Run(name, func(b *testing.B) {
				for n := 0; n < b.N; n++ {
					q := factory(capacity)
					var wg sync.WaitGroup
					totalOps := cc.producers * opsPerGoroutine

					// Producers
					wg.Add(cc.producers)
					for p := 0; p < cc.producers; p++ {
						go func(id int) {
							defer wg.Done()
							for i := 0; i < opsPerGoroutine; i++ {
								for !q.Enqueue(id*opsPerGoroutine + i) {
									// Spin
								}
							}
						}(p)
					}

					// Consumers
					consumed := make(chan struct{}, totalOps)
					wg.Add(cc.consumers)
					for c := 0; c < cc.consumers; c++ {
						go func() {
							defer wg.Done()
							for {
								if _, ok := q.Dequeue(); ok {
									consumed <- struct{}{}
									if len(consumed) >= totalOps {
										return
									}
								}
							}
						}()
					}

					wg.Wait()
				}
			})
		}
	}
}

// ===========================================================================
// Throughput Benchmark (items/second)
// ===========================================================================

// BenchmarkThroughput measures maximum single-threaded throughput.
func BenchmarkThroughput(b *testing.B) {
	const capacity = 1024

	for implName, factory := range queueImplementations {
		b.Run(implName, func(b *testing.B) {
			q := factory(capacity)
			b.ResetTimer()
			b.ReportAllocs()

			ops := 0
			for i := 0; i < b.N; i++ {
				// Enqueue batch
				for j := 0; j < capacity; j++ {
					q.Enqueue(j)
				}
				// Dequeue batch
				for j := 0; j < capacity; j++ {
					q.Dequeue()
				}
				ops += capacity * 2
			}
			b.ReportMetric(float64(ops)/b.Elapsed().Seconds(), "ops/s")
		})
	}
}
