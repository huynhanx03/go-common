package algorithm

// LCS finds the Longest Common Subsequence between s1 and s2.
func LCS(s1, s2 string) string {
	n1 := len(s1)
	n2 := len(s2)
	if n1 == 0 || n2 == 0 {
		return ""
	}

	// dp[i][j] stores length of LCS of s1[0...i-1] and s2[0...j-1]
	dp := make([][]int, n1+1)
	for i := range dp {
		dp[i] = make([]int, n2+1)
	}

	for i := 1; i <= n1; i++ {
		for j := 1; j <= n2; j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = dp[i-1][j]
				if dp[i][j-1] > dp[i][j] {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// Backtrack to find the actual string
	res := make([]byte, dp[n1][n2])
	i, j := n1, n2
	k := len(res) - 1
	for i > 0 && j > 0 {
		if s1[i-1] == s2[j-1] {
			res[k] = s1[i-1]
			i--
			j--
			k--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return string(res)
}
