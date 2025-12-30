package utils

import "strings"

// IsEmpty checks if a string is empty or contains only whitespace
func IsEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}
