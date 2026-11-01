package authentication

import (
	"crypto/rsa"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/huynhanx03/go-common/pkg/settings"
)

// VerifierOptions configures JWT verification for one service.
type VerifierOptions struct {
	Issuer        string
	Audience      string
	Leeway        time.Duration
	MaxTokenBytes int
	Clock         func() time.Time
}

// IssuerOptions configures JWT creation for one service.
type IssuerOptions struct {
	Issuer     string
	Audience   string
	KeyID      string
	PrivateKey *rsa.PrivateKey
	MaxTTL     time.Duration
	Clock      func() time.Time
}

// DefaultIssuerOptions returns issuer options with the package defaults.
// The service must still provide its issuer, audience, key ID, and private key.
func DefaultIssuerOptions() IssuerOptions {
	return (IssuerOptions{MaxTTL: hardMaxTTL}).withDefaults()
}

// DefaultVerifierOptions returns verifier options with the package defaults.
// The service must still provide its issuer, audience, and verification keys.
func DefaultVerifierOptions() VerifierOptions {
	return (VerifierOptions{}).withDefaults()
}

// DefaultJWT returns safe token policy defaults for a service configuration.
// Issuer, audience, key ID, and key material remain service-specific.
func DefaultJWT() settings.JWT {
	return settings.JWT{
		AccessTTLSeconds:  int(defaultAccessTTL.Seconds()),
		RefreshTTLSeconds: int(defaultRefreshTTL.Seconds()),
		MaxTTLSeconds:     int(hardMaxTTL.Seconds()),
		MaxTokenBytes:     defaultMaxTokenBytes,
	}
}

// NewIssuer validates options and creates an issuer with its own private key copy.
func NewIssuer(options IssuerOptions) (*Issuer, error) {
	options = options.withDefaults()
	if err := options.validate(); err != nil {
		return nil, err
	}

	privateKey := clonePrivateKey(options.PrivateKey)
	options.PrivateKey = nil
	return &Issuer{options: options, privateKey: privateKey}, nil
}

// NewVerifier validates options and creates a verifier with its own verification key copy.
func NewVerifier(keys KeySet, options VerifierOptions) (*JWTVerifier, error) {
	options = options.withDefaults()
	if err := options.validate(); err != nil {
		return nil, err
	}

	keySet, err := copyKeySet(keys)
	if err != nil {
		return nil, err
	}
	return &JWTVerifier{keys: keySet, options: options}, nil
}

func (options IssuerOptions) withDefaults() IssuerOptions {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return options
}

func (options VerifierOptions) withDefaults() VerifierOptions {
	if options.MaxTokenBytes == 0 {
		options.MaxTokenBytes = defaultMaxTokenBytes
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return options
}

func (options IssuerOptions) validate() error {
	if err := validateIssuerAudience(options.Issuer, options.Audience); err != nil {
		return err
	}
	if err := validateKeyID(options.KeyID); err != nil {
		return fmt.Errorf("%w: invalid signing key ID", ErrInvalidConfiguration)
	}
	if options.PrivateKey == nil ||
		options.PrivateKey.N == nil ||
		options.PrivateKey.N.BitLen() < minimumRSAKeyBits {
		return fmt.Errorf(
			"%w: RSA private key must be at least %d bits",
			ErrInvalidConfiguration,
			minimumRSAKeyBits,
		)
	}
	if err := options.PrivateKey.Validate(); err != nil {
		return fmt.Errorf("%w: invalid RSA private key", ErrInvalidConfiguration)
	}
	if options.MaxTTL <= 0 || options.MaxTTL > hardMaxTTL {
		return fmt.Errorf(
			"%w: maximum TTL is outside 0..%s",
			ErrInvalidConfiguration,
			hardMaxTTL,
		)
	}
	return nil
}

func (options VerifierOptions) validate() error {
	if err := validateIssuerAudience(options.Issuer, options.Audience); err != nil {
		return err
	}
	if options.Leeway < 0 || options.Leeway > hardMaxLeeway {
		return fmt.Errorf(
			"%w: verifier leeway is outside 0..%s",
			ErrInvalidConfiguration,
			hardMaxLeeway,
		)
	}
	if options.MaxTokenBytes < minimumTokenBytes || options.MaxTokenBytes > hardMaxTokenBytes {
		return fmt.Errorf(
			"%w: token byte limit is outside %d..%d",
			ErrInvalidConfiguration,
			minimumTokenBytes,
			hardMaxTokenBytes,
		)
	}
	return nil
}

func validateIssuerAudience(issuer, audience string) error {
	if err := validateIdentifier(issuerFieldName, issuer); err != nil {
		return err
	}
	return validateIdentifier(audienceFieldName, audience)
}

func validateIdentifier(name, value string) error {
	if value == "" || len(value) > maxClaimBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s is empty or invalid", ErrInvalidConfiguration, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s contains control characters", ErrInvalidConfiguration, name)
		}
	}
	return nil
}

func validateKeyID(value string) error {
	if value == "" || len(value) > maxKeyIDBytes {
		return fmt.Errorf("%w: key ID is empty or too long", ErrInvalidKeySet)
	}
	for _, character := range value {
		if !strings.ContainsRune(validKeyIDCharacters, character) {
			return fmt.Errorf("%w: key ID contains invalid characters", ErrInvalidKeySet)
		}
	}
	return nil
}

// clonePrivateKey copies a validated key and builds independent precomputed values.
func clonePrivateKey(privateKey *rsa.PrivateKey) *rsa.PrivateKey {
	cloned := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: new(big.Int).Set(privateKey.N), E: privateKey.E},
		D:         new(big.Int).Set(privateKey.D),
		Primes:    make([]*big.Int, len(privateKey.Primes)),
	}
	for index, prime := range privateKey.Primes {
		cloned.Primes[index] = new(big.Int).Set(prime)
	}
	cloned.Precompute()
	return cloned
}
