package grpc_test

import (
	"errors"
	"strings"
	"testing"

	commongrpc "github.com/huynhanx03/go-common/pkg/common/grpc"
	"github.com/huynhanx03/go-common/pkg/correlation"
)

func TestNormalizeCorrelationOptions(t *testing.T) {
	t.Parallel()

	defaults, err := commongrpc.NormalizeCorrelationOptions(commongrpc.CorrelationOptions{})
	if err != nil {
		t.Fatalf("NormalizeCorrelationOptions: %v", err)
	}
	if defaults.MetadataKey != strings.ToLower(correlation.Header) || defaults.NewID == nil {
		t.Fatalf("defaults = %+v", defaults)
	}
	explicit := commongrpc.DefaultCorrelationOptions()
	explicit.MetadataKey = "request.cid"
	normalized, err := commongrpc.NormalizeCorrelationOptions(explicit)
	if err != nil || normalized.MetadataKey != "request.cid" {
		t.Fatalf("normalized=%+v error=%v", normalized, err)
	}

	for _, key := range []string{
		"Uppercase",
		"contains space",
		"grpc-reserved",
		strings.Repeat("a", 65),
	} {
		_, err := commongrpc.NormalizeCorrelationOptions(commongrpc.CorrelationOptions{
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
		defaults.MaxCredentialBytes != commongrpc.DefaultMaxCredentialBytes ||
		defaults.Now == nil {
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
