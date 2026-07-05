package algorithm

import "testing"

func TestBinarySearch(t *testing.T) {
	tests := []struct {
		name string
		l    int
		r    int
		f    func(int) bool
		want int
	}{
		{
			name: "find_boundary",
			l:    0, r: 9,
			f:    func(i int) bool { return i >= 5 },
			want: 5,
		},
		{
			name: "all_true",
			l:    0, r: 9,
			f:    func(int) bool { return true },
			want: 0,
		},
		{
			name: "all_false",
			l:    0, r: 9,
			f:    func(int) bool { return false },
			want: 10,
		},
		{
			name: "single_true",
			l:    3, r: 3,
			f:    func(int) bool { return true },
			want: 3,
		},
		{
			name: "single_false",
			l:    3, r: 3,
			f:    func(int) bool { return false },
			want: 4,
		},
		{
			name: "empty_range",
			l:    5, r: 3,
			f:    func(int) bool { return true },
			want: 5,
		},
		{
			name: "sorted_array_lower_bound",
			l:    0, r: 4,
			f: func(i int) bool {
				sorted := []int{1, 3, 5, 7, 9}
				return sorted[i] >= 5
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BinarySearch(tt.l, tt.r, tt.f)
			if got != tt.want {
				t.Errorf("BinarySearch(%d, %d, f) = %d, want %d", tt.l, tt.r, got, tt.want)
			}
		})
	}
}

func BenchmarkBinarySearch(b *testing.B) {
	sorted := make([]int, 10000)
	for i := range sorted {
		sorted[i] = i * 2
	}
	target := 9998

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BinarySearch(0, len(sorted)-1, func(i int) bool {
			return sorted[i] >= target
		})
	}
}
