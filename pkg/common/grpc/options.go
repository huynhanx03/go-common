// Package grpc contains transport-level contracts shared by the gRPC
// interceptors. Application-specific methods, resources, and roles do not
// belong here.
package grpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

const (
	DefaultCredentialMetadataKey = "authorization"
	DefaultCredentialScheme      = "bearer"
	DefaultMaxCredentialBytes    = 16 << 10
	HardMaxCredentialBytes       = 64 << 10
	maxMetadataKeyBytes          = 64
	maxSchemeBytes               = 32
)

var ErrInvalidConfiguration = errors.New("grpc: invalid configuration")

// CorrelationOptions controls the single correlation metadata entry. A zero
// value resolves to safe defaults.
type CorrelationOptions struct {
	MetadataKey string
	NewID       func() string
}

func DefaultCorrelationOptions() CorrelationOptions {
	return CorrelationOptions{
		MetadataKey: strings.ToLower(correlation.Header),
		NewID:       correlation.New,
	}
}

func NormalizeCorrelationOptions(input CorrelationOptions) (CorrelationOptions, error) {
	if input.MetadataKey == "" {
		input.MetadataKey = strings.ToLower(correlation.Header)
	}
	if input.NewID == nil {
		input.NewID = correlation.New
	}
	if !validMetadataKey(input.MetadataKey) {
		return CorrelationOptions{}, ErrInvalidConfiguration
	}
	return input, nil
}

// MetadataSource describes one bounded incoming credential. The zero value is
// required bearer authentication from the standard authorization metadata.
type MetadataSource struct {
	MetadataKey        string
	Scheme             string
	Optional           bool
	MaxCredentialBytes int
	Now                func() time.Time
}

func DefaultBearerMetadataSource() MetadataSource {
	return MetadataSource{
		MetadataKey:        DefaultCredentialMetadataKey,
		Scheme:             DefaultCredentialScheme,
		MaxCredentialBytes: DefaultMaxCredentialBytes,
		Now:                time.Now,
	}
}

func NormalizeMetadataSource(input MetadataSource) (MetadataSource, error) {
	if input.MetadataKey == "" {
		input.MetadataKey = DefaultCredentialMetadataKey
	}
	if input.Scheme == "" {
		input.Scheme = DefaultCredentialScheme
	}
	if input.MaxCredentialBytes == 0 {
		input.MaxCredentialBytes = DefaultMaxCredentialBytes
	}
	if input.Now == nil {
		input.Now = time.Now
	}
	input.Scheme = strings.ToLower(input.Scheme)
	if !validMetadataKey(input.MetadataKey) ||
		!validToken(input.Scheme, maxSchemeBytes) ||
		input.MaxCredentialBytes <= 0 ||
		input.MaxCredentialBytes > HardMaxCredentialBytes {
		return MetadataSource{}, ErrInvalidConfiguration
	}
	return input, nil
}

// Permission is resolved by the application from a method and request. It is
// deliberately free of service-specific roles, domains, and object facts.
type Permission struct {
	Resource string
	Action   string
}

type PermissionResolver func(
	ctx context.Context,
	fullMethod string,
	request any,
) (Permission, error)

func validMetadataKey(value string) bool {
	if value == "" ||
		len(value) > maxMetadataKeyBytes ||
		value != strings.ToLower(value) ||
		strings.HasPrefix(value, "grpc-") {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
