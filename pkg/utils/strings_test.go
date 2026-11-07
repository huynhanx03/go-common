package utils

import "testing"

func TestStringHelpersFollowWhitespaceAndRuneBoundaries(t *testing.T) {
	for _, input := range []string{"", " \t\n", "\u2003\u00a0"} {
		if !IsEmpty(input) {
			t.Errorf("IsEmpty(%q) = false", input)
		}
	}
	if IsEmpty(" x ") {
		t.Fatal("IsEmpty treated a non-whitespace value as empty")
	}
	for input, want := range map[string]string{
		"HTTPServer": "h_t_t_p_server",
		"UserID":     "user_i_d",
		"Étage":      "étage",
		"überHTTP":   "über_h_t_t_p",
	} {
		if got := ToSnakeCase(input); got != want {
			t.Errorf("ToSnakeCase(%q) = %q, want %q", input, got, want)
		}
	}
}
