package unique

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"testing"
)

func TestNewToken(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	if len(token) != 32 {
		t.Fatalf("token length = %d, want 32 hex characters", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
}

func TestNewTokenReturnsRandomSourceError(t *testing.T) {
	previous := rand.Reader
	rand.Reader = errorReader{}
	t.Cleanup(func() { rand.Reader = previous })
	if _, err := NewToken(); err == nil {
		t.Fatal("NewToken() error = nil with failing random source")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
