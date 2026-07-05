package unique

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/encoding/base62"
)

func TestRandBase62(t *testing.T) {
	s := RandBase62(64)
	if len(s) != 64 {
		t.Fatalf("len = %d, want 64", len(s))
	}
	for _, c := range s {
		if !strings.ContainsRune(base62.Alphabet, c) {
			t.Fatalf("character %q outside base62 alphabet", c)
		}
	}
	if RandBase62(0) != "" {
		t.Fatal("RandBase62(0) must be empty")
	}
}

func TestPublicIDFormat(t *testing.T) {
	at := time.Date(2026, 7, 5, 23, 59, 0, 0, time.UTC)
	id := PublicID("UR", at, 3)
	if !regexp.MustCompile(`^UR20260705[0-9A-Za-z]{3}$`).MatchString(id) {
		t.Fatalf("unexpected format: %q", id)
	}
}

func TestPublicIDUsesUTCDate(t *testing.T) {
	// 23:00 in UTC+7 is already the next day locally; the ID must use UTC.
	local := time.Date(2026, 7, 5, 23, 0, 0, 0, time.FixedZone("UTC+7", 7*3600))
	if id := PublicID("UR", local, 3); !strings.HasPrefix(id, "UR20260705") {
		t.Fatalf("date not converted to UTC: %q", id)
	}
}

func TestPublicIDRandomSuffix(t *testing.T) {
	at := time.Now()
	seen := make(map[string]struct{})
	for range 100 {
		seen[PublicID("X", at, 8)] = struct{}{}
	}
	if len(seen) != 100 {
		t.Fatalf("collisions in 100 draws of 62^8 space: %d unique", len(seen))
	}
}
