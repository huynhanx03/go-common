package middlewares

import "testing"

func TestRedactQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain params untouched", "page=2&limit=10", "page=2&limit=10"},
		{"token masked", "token=abc123", "token=REDACTED"},
		{"mixed keeps others", "page=2&access_token=abc", "access_token=REDACTED&page=2"},
		{"password masked", "password=hunter2", "password=REDACTED"},
		{"api key masked", "api_key=xyz", "api_key=REDACTED"},
		{"case insensitive", "Authorization=Bearer1", "Authorization=REDACTED"},
		{"oauth code masked", "code=4%2F0AX4", "code=REDACTED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactQuery(tc.in); got != tc.want {
				t.Errorf("redactQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsSensitiveParam(t *testing.T) {
	for _, key := range []string{"token", "refresh_token", "PASSWORD", "client_secret", "auth", "otp", "signature"} {
		if !isSensitiveParam(key) {
			t.Errorf("isSensitiveParam(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"page", "limit", "sort", "keyword", "user_id"} {
		if isSensitiveParam(key) {
			t.Errorf("isSensitiveParam(%q) = true, want false", key)
		}
	}
}
