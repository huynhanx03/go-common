package algorithm

import "testing"

func TestLCS(t *testing.T) {
	tests := []struct {
		name string
		s1   string
		s2   string
		want string
	}{
		{"happy_subsequence", "abcde", "ace", "ace"},
		{"happy_longer", "ABCBDAB", "BDCAB", "BDAB"},
		{"both_empty", "", "", ""},
		{"first_empty", "", "abc", ""},
		{"second_empty", "abc", "", ""},
		{"identical", "abc", "abc", "abc"},
		{"no_common", "abc", "xyz", ""},
		{"single_char_match", "a", "a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LCS(tt.s1, tt.s2)
			if got != tt.want {
				t.Errorf("LCS(%q, %q) = %q, want %q", tt.s1, tt.s2, got, tt.want)
			}
		})
	}
}

func BenchmarkLCS(b *testing.B) {
	s1 := "ABCBDABXYZQWERTY"
	s2 := "BDCABQWERTYXYZ"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LCS(s1, s2)
	}
}
