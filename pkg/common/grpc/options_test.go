package grpc_test

import (
	"errors"
	"strings"
	"testing"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestNormalizeRequestIDOptions(t *testing.T) {
	t.Parallel()

	defaults, err := commongrpc.NormalizeRequestIDOptions(commongrpc.RequestIDOptions{})
	if err != nil {
		t.Fatalf("NormalizeRequestIDOptions: %v", err)
	}
	if defaults.MetadataKey != strings.ToLower(logger.RequestIDHeader) {
		t.Fatalf("defaults = %+v", defaults)
	}
	explicit := commongrpc.DefaultRequestIDOptions()
	explicit.MetadataKey = "request.correlation-key"
	normalized, err := commongrpc.NormalizeRequestIDOptions(explicit)
	if err != nil || normalized.MetadataKey != "request.correlation-key" {
		t.Fatalf("normalized=%+v error=%v", normalized, err)
	}

	for _, key := range []string{
		"Uppercase",
		"contains space",
		"grpc-reserved",
		strings.Repeat("a", 65),
	} {
		_, err := commongrpc.NormalizeRequestIDOptions(commongrpc.RequestIDOptions{
			MetadataKey: key,
		})
		if !errors.Is(err, commongrpc.ErrInvalidConfiguration) {
			t.Fatalf("key %q error = %v", key, err)
		}
	}
}

func TestNormalizeMetadataSource(t *testing.T) {
	t.Parallel()

	defaults, err := commongrpc.NormalizeMetadataSource(commongrpc.MetadataSource{})
	if err != nil {
		t.Fatalf("NormalizeMetadataSource: %v", err)
	}
	if defaults.MetadataKey != commongrpc.DefaultCredentialMetadataKey ||
		defaults.Scheme != commongrpc.DefaultCredentialScheme ||
		defaults.MaxCredentialBytes != commongrpc.DefaultMaxCredentialBytes {
		t.Fatalf("defaults = %+v", defaults)
	}
	explicit := commongrpc.DefaultBearerMetadataSource()
	explicit.Scheme = "Bearer"
	normalized, err := commongrpc.NormalizeMetadataSource(explicit)
	if err != nil || normalized.Scheme != "bearer" {
		t.Fatalf("normalized=%+v error=%v", normalized, err)
	}

	for _, source := range []commongrpc.MetadataSource{
		{MetadataKey: "Uppercase"},
		{MetadataKey: "grpc-auth"},
		{Scheme: "bad scheme"},
		{MaxCredentialBytes: -1},
		{MaxCredentialBytes: commongrpc.HardMaxCredentialBytes + 1},
	} {
		if _, err := commongrpc.NormalizeMetadataSource(source); !errors.Is(
			err,
			commongrpc.ErrInvalidConfiguration,
		) {
			t.Fatalf("source %+v error = %v", source, err)
		}
	}
}
