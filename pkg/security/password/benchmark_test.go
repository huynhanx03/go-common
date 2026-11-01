//go:build benchmark

package password

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

var benchmarkHash string
var benchmarkVerification Verification

var errBenchmarkVerification = errors.New("verification rejected benchmark password")

func BenchmarkHash(b *testing.B) {
	for _, profile := range []struct {
		name        string
		memoryKiB   uint32
		iterations  uint32
		parallelism uint8
	}{
		{name: "8MiB-1Iteration", memoryKiB: 8 * 1024, iterations: 1, parallelism: 1},
		{name: "current-default", memoryKiB: defaultMemoryKiB, iterations: defaultIterations, parallelism: defaultParallelism},
	} {
		b.Run(profile.name, func(b *testing.B) {
			config := DefaultPassword()
			config.MemoryKiB = profile.memoryKiB
			config.Iterations = profile.iterations
			config.Parallelism = profile.parallelism
			hasher, err := New(config)
			if err != nil {
				b.Fatal(err)
			}
			password := []byte("benchmark password")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				encoded, err := hasher.Hash(context.Background(), password)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkHash = encoded
			}
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	for _, profile := range []struct {
		name        string
		memoryKiB   uint32
		iterations  uint32
		parallelism uint8
	}{
		{name: "8MiB-1Iteration", memoryKiB: 8 * 1024, iterations: 1, parallelism: 1},
		{name: "current-default", memoryKiB: defaultMemoryKiB, iterations: defaultIterations, parallelism: defaultParallelism},
	} {
		b.Run(profile.name, func(b *testing.B) {
			config := DefaultPassword()
			config.MemoryKiB = profile.memoryKiB
			config.Iterations = profile.iterations
			config.Parallelism = profile.parallelism
			hasher, err := New(config)
			if err != nil {
				b.Fatal(err)
			}
			password := []byte("benchmark password")
			encoded, err := hasher.Hash(context.Background(), password)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result, err := hasher.Verify(context.Background(), encoded, password)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkVerification = result
			}
		})
	}
}

func BenchmarkLoginVerifyConcurrent(b *testing.B) {
	config := DefaultPassword()
	hasher, err := New(config)
	if err != nil {
		b.Fatal(err)
	}
	password := []byte("benchmark password")
	encoded, err := hasher.Hash(context.Background(), password)
	if err != nil {
		b.Fatal(err)
	}

	for _, requestCount := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("requests-%d", requestCount), func(b *testing.B) {
			var group sync.WaitGroup
			errors := make(chan error, requestCount)
			b.ReportAllocs()
			b.ResetTimer()
			for worker := range requestCount {
				operations := b.N / requestCount
				if worker < b.N%requestCount {
					operations++
				}
				group.Add(1)
				go func() {
					defer group.Done()
					for range operations {
						result, verifyErr := hasher.Verify(context.Background(), encoded, password)
						if verifyErr != nil {
							errors <- verifyErr
							return
						}
						if !result.Valid {
							errors <- errBenchmarkVerification
							return
						}
					}
				}()
			}
			group.Wait()
			b.StopTimer()
			close(errors)
			for verifyErr := range errors {
				b.Fatal(verifyErr)
			}
		})
	}
}
