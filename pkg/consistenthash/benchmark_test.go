package consistenthash

import (
	"fmt"
	"testing"
)

func BenchmarkLookupHash(b *testing.B) {
	for _, members := range []int{128, 512} {
		b.Run(fmt.Sprintf("%d-members", members), func(b *testing.B) {
			r := benchmarkRing(b, members)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = r.LookupHash(uint64(i) * 0x9e3779b97f4a7c15)
			}
		})
	}
}
func BenchmarkLookup(b *testing.B) {
	r := benchmarkRing(b, 128)
	key := []byte("tenant/user/resource")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Lookup(key)
	}
}
func benchmarkRing(b *testing.B, count int) *Ring[string] {
	b.Helper()
	r, err := New[string](Options{VirtualNodes: 16})
	if err != nil {
		b.Fatal(err)
	}
	m := make([]Member[string], count)
	for i := range m {
		m[i].ID = fmt.Sprintf("node-%05d", i)
	}
	if err := r.Replace(m); err != nil {
		b.Fatal(err)
	}
	return r
}
