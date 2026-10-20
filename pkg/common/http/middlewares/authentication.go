package middlewares

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	authenticationjwt "github.com/huynhanx03/go-common/pkg/security/authentication/jwt"
)

const (
	defaultMaxCredentialBytes = 16 << 10
	hardMaxCredentialBytes    = 64 << 10
	legacyJWTIssuer           = "go-common-legacy"
	legacyJWTAudience         = "go-common-legacy-consumer"
	legacyJWTKeyID            = "legacy-rs256"
)

// CredentialSource identifies the HTTP location from which a credential was
// extracted. Raw credential values are never stored in context.
type CredentialSource uint8

const (
	CredentialBearer CredentialSource = iota + 1
	CredentialCookie
)

type authenticationConfig struct {
	sources            map[CredentialSource]struct{}
	cookieName         string
	maxCredentialBytes int
	expectedPurpose    authentication.TokenPurpose
	legacyContext      bool
	now                func() time.Time
	err                error
}

// AuthenticationOption configures credential extraction and compatibility.
type AuthenticationOption func(*authenticationConfig)

// WithExpectedTokenPurpose selects the credential purpose verified at this
// route boundary. Access is the safe default; refresh endpoints must opt in to
// PurposeRefresh so an access credential can never be used to mint another.
func WithExpectedTokenPurpose(purpose authentication.TokenPurpose) AuthenticationOption {
	return func(config *authenticationConfig) {
		if err := purpose.Validate(); err != nil {
			config.err = errors.New("authentication middleware: invalid token purpose")
			return
		}
		config.expectedPurpose = purpose
	}
}

// WithCredentialSources selects accepted credential locations. The default is
// bearer-only.
func WithCredentialSources(sources ...CredentialSource) AuthenticationOption {
	return func(config *authenticationConfig) {
		selected := make(map[CredentialSource]struct{}, len(sources))
		for _, source := range sources {
			if source != CredentialBearer && source != CredentialCookie {
				config.err = errors.New("authentication middleware: invalid credential source")
				return
			}
			if _, duplicate := selected[source]; duplicate {
				config.err = errors.New("authentication middleware: duplicate credential source")
				return
			}
			selected[source] = struct{}{}
		}
		if len(selected) == 0 {
			config.err = errors.New("authentication middleware: no credential sources")
			return
		}
		config.sources = selected
	}
}

// WithCredentialCookie sets the bounded cookie name used when CredentialCookie
// is enabled.
func WithCredentialCookie(name string) AuthenticationOption {
	return func(config *authenticationConfig) {
		if !validHTTPToken(name) {
			config.err = errors.New("authentication middleware: invalid cookie name")
			return
		}
		config.cookieName = name
	}
}

// WithMaxCredentialBytes bounds a credential before the verifier is invoked.
func WithMaxCredentialBytes(maximum int) AuthenticationOption {
	return func(config *authenticationConfig) {
		if maximum <= 0 || maximum > hardMaxCredentialBytes {
			config.err = errors.New("authentication middleware: invalid credential limit")
			return
		}
		config.maxCredentialBytes = maximum
	}
}

// WithLegacyAuthenticationContext explicitly mirrors authenticated user ID and
// username values into the deprecated constraints context keys.
func WithLegacyAuthenticationContext() AuthenticationOption {
	return func(config *authenticationConfig) {
		config.legacyContext = true
	}
}

// WithAuthenticationClock overrides principal-boundary validation time.
func WithAuthenticationClock(now func() time.Time) AuthenticationOption {
	return func(config *authenticationConfig) {
		if now == nil {
			config.err = errors.New("authentication middleware: nil clock")
			return
		}
		config.now = now
	}
}

type credentialResult struct {
	raw     string
	present bool
	source  CredentialSource
}

type credentialSourceContextKey struct{}

// CredentialSourceFromContext returns transport metadata without exposing the
// credential itself.
func CredentialSourceFromContext(ctx context.Context) (CredentialSource, bool) {
	if ctx == nil {
		return 0, false
	}
	source, ok := ctx.Value(credentialSourceContextKey{}).(CredentialSource)
	return source, ok
}

// AuthenticationWithVerifier requires a valid access credential.
func AuthenticationWithVerifier(
	verifier authentication.Verifier,
	options ...AuthenticationOption,
) gin.HandlerFunc {
	return authenticationMiddleware(verifier, false, options...)
}

// OptionalAuthenticationWithVerifier stores an anonymous principal only when
// no credential is present. Malformed or invalid credentials always return
// 401 rather than silently downgrading the request.
func OptionalAuthenticationWithVerifier(
	verifier authentication.Verifier,
	options ...AuthenticationOption,
) gin.HandlerFunc {
	return authenticationMiddleware(verifier, true, options...)
}

func authenticationMiddleware(
	verifier authentication.Verifier,
	optional bool,
	options ...AuthenticationOption,
) gin.HandlerFunc {
	config := authenticationConfig{
		sources:            map[CredentialSource]struct{}{CredentialBearer: {}},
		maxCredentialBytes: defaultMaxCredentialBytes,
		expectedPurpose:    authentication.PurposeAccess,
		now:                time.Now,
	}
	for _, option := range options {
		if option == nil {
			config.err = errors.New("authentication middleware: nil option")
			continue
		}
		option(&config)
	}
	if _, cookieEnabled := config.sources[CredentialCookie]; cookieEnabled && config.cookieName == "" {
		config.err = errors.New("authentication middleware: cookie source has no name")
	}
	if isNilVerifier(verifier) {
		config.err = errors.New("authentication middleware: nil verifier")
	}

	return func(c *gin.Context) {
		if config.err != nil {
			internalServerError(c)
			return
		}

		credential, err := extractCredential(c.Request, config)
		if err != nil {
			unauthorized(c)
			return
		}
		if !credential.present {
			if !optional {
				unauthorized(c)
				return
			}
			ctx := authentication.WithPrincipal(c.Request.Context(), authentication.Anonymous())
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return
		}

		principal, err := verifier.Verify(c.Request.Context(), credential.raw, config.expectedPurpose)
		if err != nil {
			if c.Request.Context().Err() != nil {
				c.Abort()
				return
			}
			unauthorized(c)
			return
		}
		if err := principal.Validate(config.now().UTC()); err != nil || !principal.Authenticated {
			unauthorized(c)
			return
		}

		ctx := authentication.WithPrincipal(c.Request.Context(), principal)
		ctx = context.WithValue(ctx, credentialSourceContextKey{}, credential.source)
		if config.legacyContext {
			ctx = context.WithValue(ctx, constraints.ContextKeyUserID, principal.UserID)
			ctx = context.WithValue(ctx, constraints.ContextKeyUsername, principal.Username)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func extractCredential(request *http.Request, config authenticationConfig) (credentialResult, error) {
	var found []credentialResult

	authorizationValues := request.Header.Values(constraints.HeaderAuthorization)
	if len(authorizationValues) > 1 {
		return credentialResult{}, authentication.ErrInvalidToken
	}
	if len(authorizationValues) == 1 {
		if _, enabled := config.sources[CredentialBearer]; !enabled {
			return credentialResult{}, authentication.ErrInvalidToken
		}
		parts := strings.Fields(authorizationValues[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], constraints.TokenTypeBearer) {
			return credentialResult{}, authentication.ErrInvalidToken
		}
		if err := validateRawCredential(parts[1], config.maxCredentialBytes); err != nil {
			return credentialResult{}, err
		}
		found = append(found, credentialResult{raw: parts[1], present: true, source: CredentialBearer})
	}

	if config.cookieName != "" {
		cookies := request.CookiesNamed(config.cookieName)
		if len(cookies) > 1 {
			return credentialResult{}, authentication.ErrInvalidToken
		}
		if len(cookies) == 1 {
			if _, enabled := config.sources[CredentialCookie]; !enabled {
				return credentialResult{}, authentication.ErrInvalidToken
			}
			if err := validateRawCredential(cookies[0].Value, config.maxCredentialBytes); err != nil {
				return credentialResult{}, err
			}
			found = append(found, credentialResult{raw: cookies[0].Value, present: true, source: CredentialCookie})
		}
	}

	if len(found) == 0 {
		return credentialResult{}, nil
	}
	if len(found) != 1 {
		return credentialResult{}, authentication.ErrInvalidToken
	}
	return found[0], nil
}

func validateRawCredential(raw string, maximum int) error {
	if raw == "" || len(raw) > maximum || strings.TrimSpace(raw) != raw {
		return authentication.ErrInvalidToken
	}
	for _, character := range raw {
		if character <= 0x20 || character == 0x7f {
			return authentication.ErrInvalidToken
		}
	}
	return nil
}

func unauthorized(c *gin.Context) {
	response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(
		apperr.CodeUnauthorized,
		"unauthorized",
		nil,
	))
	c.Abort()
}

func internalServerError(c *gin.Context) {
	response.ErrorResponse(c, apperr.CodeInternalServer, apperr.New(
		apperr.CodeInternalServer,
		"internal server error",
		nil,
	))
	c.Abort()
}

// Authentication validates legacy go-common RS256 tokens.
//
// Deprecated: construct a typed JWT verifier and use
// AuthenticationWithVerifier.

func Authentication(publicKey interface{}, options ...AuthenticationOption) gin.HandlerFunc {
	options = append(options, WithLegacyAuthenticationContext())
	return AuthenticationWithVerifier(
		legacyJWTVerifier(publicKey),
		options...,
	)
}

// OptionalAuthentication validates legacy go-common RS256 tokens when
// present. Invalid credentials return 401.
//
// Deprecated: construct a typed JWT verifier and use
// OptionalAuthenticationWithVerifier.

func OptionalAuthentication(publicKey interface{}, options ...AuthenticationOption) gin.HandlerFunc {
	options = append(options, WithLegacyAuthenticationContext())
	return OptionalAuthenticationWithVerifier(
		legacyJWTVerifier(publicKey),
		options...,
	)
}

func legacyJWTVerifier(publicKey interface{}) authentication.Verifier {
	key, ok := publicKey.(*rsa.PublicKey)
	if !ok || key == nil {
		return verifierError{err: authentication.ErrInvalidConfiguration}
	}
	verifier, err := authenticationjwt.NewVerifier(
		authenticationjwt.KeySet{Active: map[string]*rsa.PublicKey{legacyJWTKeyID: key}},
		authenticationjwt.VerifierOptions{
			Issuer:   legacyJWTIssuer,
			Audience: legacyJWTAudience,
		},
	)
	if err != nil {
		return verifierError{err: err}
	}
	return verifier
}

type verifierError struct {
	err error
}

func (verifierError verifierError) Verify(
	context.Context,
	string,
	authentication.TokenPurpose,
) (authentication.Principal, error) {
	return authentication.Anonymous(), authentication.ErrInvalidConfiguration
}

func isNilVerifier(verifier authentication.Verifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validHTTPToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}
