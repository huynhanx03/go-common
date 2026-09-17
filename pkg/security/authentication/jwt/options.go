package jwt

import (
	"crypto/rsa"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

const (
	defaultMaxTokenBytes = 16 << 10
	hardMaxTokenBytes    = 64 << 10
	hardMaxLeeway        = 5 * time.Minute
	maxClaimBytes        = 256
	maxKeyIDBytes        = 128
)

type VerifierOptions struct {
	Issuer        string
	Audience      string
	Leeway        time.Duration
	MaxTokenBytes int
	Clock         func() time.Time
}

type IssuerOptions struct {
	Issuer     string
	Audience   string
	KeyID      string
	PrivateKey *rsa.PrivateKey
	MaxTTL     time.Duration
	Clock      func() time.Time
}

func normalizeVerifierOptions(options VerifierOptions) (VerifierOptions, error) {
	if err := validateIdentifier("issuer", options.Issuer); err != nil {
		return VerifierOptions{}, err
	}
	if err := validateIdentifier("audience", options.Audience); err != nil {
		return VerifierOptions{}, err
	}
	if options.Leeway < 0 || options.Leeway > hardMaxLeeway {
		return VerifierOptions{}, fmt.Errorf(
			"%w: verifier leeway is outside 0..%s",
			authentication.ErrInvalidConfiguration,
			hardMaxLeeway,
		)
	}
	if options.MaxTokenBytes == 0 {
		options.MaxTokenBytes = defaultMaxTokenBytes
	}
	if options.MaxTokenBytes < 256 || options.MaxTokenBytes > hardMaxTokenBytes {
		return VerifierOptions{}, fmt.Errorf(
			"%w: token byte limit is outside 256..%d",
			authentication.ErrInvalidConfiguration,
			hardMaxTokenBytes,
		)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return options, nil
}

func validateIdentifier(name, value string) error {
	if value == "" || len(value) > maxClaimBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf(
			"%w: %s is empty or invalid",
			authentication.ErrInvalidConfiguration,
			name,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf(
				"%w: %s contains control characters",
				authentication.ErrInvalidConfiguration,
				name,
			)
		}
	}
	return nil
}

func validateKeyID(value string) error {
	if value == "" || len(value) > maxKeyIDBytes {
		return fmt.Errorf("%w: key ID is empty or too long", authentication.ErrInvalidKeySet)
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' ||
			character == '_' ||
			character == ':' ||
			character == '-') {
			return fmt.Errorf("%w: key ID contains invalid characters", authentication.ErrInvalidKeySet)
		}
	}
	return nil
}

func validateClaim(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is required", authentication.ErrInvalidToken, name)
		}
		return nil
	}
	if len(value) > maxClaimBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s is invalid", authentication.ErrInvalidToken, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s is invalid", authentication.ErrInvalidToken, name)
		}
	}
	return nil
}
