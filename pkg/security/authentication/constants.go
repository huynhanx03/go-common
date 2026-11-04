package authentication

import "time"

const (
	PurposeAccess  TokenPurpose = "access"
	PurposeRefresh TokenPurpose = "refresh"

	minimumTokenBytes = 256
	minimumRSAKeyBits = 2048

	defaultAccessTTL     = 15 * time.Minute
	defaultMaxTokenBytes = 16 << 10
	defaultRefreshTTL    = 7 * 24 * time.Hour
	hardMaxTokenBytes    = 64 << 10
	hardMaxLeeway        = 5 * time.Minute
	hardMaxTTL           = 30 * 24 * time.Hour
	maxClaimBytes        = 256
	maxIssueClockSkew    = time.Minute
	maxKeyIDBytes        = 128

	issuerFieldName    = "issuer"
	audienceFieldName  = "audience"
	subjectFieldName   = "subject"
	userIDFieldName    = "user ID"
	sessionIDFieldName = "session ID"
	usernameFieldName  = "username"
	tokenIDFieldName   = "token ID"

	jwtKeyIDHeader       = "kid"
	validKeyIDCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-"
)
