package temporal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/api/serviceerror"
	sdkclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestClientConfigurationValidation(t *testing.T) {
	log := logger.Wrap(zap.NewNop())
	valid := validTemporalSettings()

	tests := []struct {
		name   string
		config settings.Temporal
		want   error
	}{
		{name: "missing endpoint", config: settings.Temporal{Namespace: valid.Namespace}, want: ErrInvalidConfig},
		{name: "missing namespace", config: settings.Temporal{Address: valid.Address}, want: ErrInvalidConfig},
		{name: "API key requires TLS", config: settings.Temporal{Address: valid.Address, Namespace: valid.Namespace, APIKey: "secret"}, want: ErrInvalidConfig},
		{name: "client key requires certificate", config: settings.Temporal{Address: valid.Address, Namespace: valid.Namespace, TLSEnabled: true, KeyFile: "key.pem"}, want: ErrInvalidConfig},
		{name: "TLS fields require TLS", config: settings.Temporal{Address: valid.Address, Namespace: valid.Namespace, ServerName: "temporal.example"}, want: ErrInvalidConfig},
		{name: "negative dial timeout", config: settings.Temporal{Address: valid.Address, Namespace: valid.Namespace, DialTimeoutSeconds: -1}, want: ErrInvalidConfig},
		{name: "dial timeout exceeds the supported budget", config: settings.Temporal{Address: valid.Address, Namespace: valid.Namespace, DialTimeoutSeconds: int(maximumTemporalTimeout/time.Second) + 1}, want: ErrInvalidConfig},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Dial(context.Background(), log, test.config)
			if !errors.Is(err, test.want) {
				t.Fatalf("Dial() error = %v; want %v", err, test.want)
			}
		})
	}

	t.Run("malformed CA is rejected before dialing", func(t *testing.T) {
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
			t.Fatalf("write malformed CA file: %v", err)
		}
		config := valid
		config.TLSEnabled = true
		config.CAFile = caFile
		_, err := Dial(context.Background(), log, config)
		if !errors.Is(err, ErrInvalidTLSConfig) {
			t.Fatalf("Dial() error = %v; want ErrInvalidTLSConfig", err)
		}
	})
}

func TestClientLoadsTrustedRootsAndClientCertificate(t *testing.T) {
	certificatePath, keyPath, caPath := writeTemporaryTLSIdentity(t)
	config := validTemporalSettings()
	config.TLSEnabled = true
	config.ServerName = "temporal.internal"
	config.CAFile = caPath
	config.CertFile = certificatePath
	config.KeyFile = keyPath

	var received sdkclient.Options
	_, err := dial(context.Background(), logger.Wrap(zap.NewNop()), config, nil, func(_ context.Context, options sdkclient.Options) (sdkclient.Client, error) {
		received = options
		return &testSDKClient{}, nil
	})
	if err != nil {
		t.Fatalf("Dial with valid mutual TLS files returned an error: %v", err)
	}
	tlsConfig := received.ConnectionOptions.TLS
	if tlsConfig == nil || tlsConfig.MinVersion != tls.VersionTLS12 || tlsConfig.ServerName != config.ServerName || tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("client TLS configuration = %+v", tlsConfig)
	}
}

func TestClientRejectsUnreadableTLSFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		config func(string) settings.Temporal
	}{
		{name: "missing CA file", config: func(path string) settings.Temporal {
			config := validTemporalSettings()
			config.TLSEnabled = true
			config.CAFile = path
			return config
		}},
		{name: "unreadable client key pair", config: func(path string) settings.Temporal {
			config := validTemporalSettings()
			config.TLSEnabled = true
			config.CertFile = path
			config.KeyFile = path
			return config
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Dial(context.Background(), logger.Wrap(zap.NewNop()), test.config(filepath.Join(t.TempDir(), "missing.pem")))
			if !errors.Is(err, ErrInvalidTLSConfig) {
				t.Fatalf("Dial with unreadable TLS input returned %v; want invalid TLS config", err)
			}
		})
	}
}

func writeTemporaryTLSIdentity(t *testing.T) (certificatePath, keyPath, caPath string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "temporal test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	certificatePath = filepath.Join(t.TempDir(), "client.pem")
	keyPath = filepath.Join(t.TempDir(), "client-key.pem")
	caPath = filepath.Join(t.TempDir(), "ca.pem")
	for path, contents := range map[string][]byte{
		certificatePath: certificatePEM,
		keyPath:         privateKeyPEM,
		caPath:          certificatePEM,
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return certificatePath, keyPath, caPath
}

func TestClientDialContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Dial(ctx, logger.Wrap(zap.NewNop()), validTemporalSettings())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial() error = %v; want context.Canceled", err)
	}
}

func TestClientOptionComposition(t *testing.T) {
	ctx := context.Background()
	base, observed := observer.New(zap.DebugLevel)
	commonLogger := logger.Wrap(zap.New(base))
	originalPropagator := &testContextPropagator{}
	mutatedPropagator := &testContextPropagator{}
	propagatorBacking := make([]workflow.ContextPropagator, 1, 4)
	propagatorBacking[0] = originalPropagator
	applicationInterceptor := &testClientInterceptor{}
	interceptorBacking := make([]interceptor.ClientInterceptor, 1, 4)
	interceptorBacking[0] = applicationInterceptor
	nativeOptions := sdkclient.Options{
		HostPort:                "native.example:7233",
		Namespace:               "native-namespace",
		ConnectionOptions:       sdkclient.ConnectionOptions{TLS: &tls.Config{ServerName: "native.example"}},
		ContextPropagators:      propagatorBacking,
		Interceptors:            interceptorBacking,
		WorkerHeartbeatInterval: 15 * time.Second,
	}
	clientOption := WithSDKOptions(nativeOptions)
	nativeOptions.ContextPropagators[0] = mutatedPropagator
	provider := trace.NewTracerProvider()
	defer provider.Shutdown(ctx)

	config := validTemporalSettings()
	config.Address = "configured.example:7233"
	config.Namespace = "configured-namespace"
	config.TLSEnabled = true
	config.ServerName = "configured.example"

	var received sdkclient.Options
	upstream := &testSDKClient{}
	wrapped, err := dial(ctx, commonLogger, config, []Option{clientOption, WithTracing(provider)}, func(_ context.Context, options sdkclient.Options) (sdkclient.Client, error) {
		received = options
		return upstream, nil
	})
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	if wrapped.SDK() != upstream {
		t.Fatal("SDK() did not return the connected client")
	}
	if received.HostPort != config.Address || received.Namespace != config.Namespace {
		t.Fatalf("common connection settings were not applied: host=%q namespace=%q", received.HostPort, received.Namespace)
	}
	if received.ConnectionOptions.TLS == nil || received.ConnectionOptions.TLS.ServerName != config.ServerName {
		t.Fatalf("common TLS settings were not applied: %#v", received.ConnectionOptions.TLS)
	}
	if received.WorkerHeartbeatInterval != nativeOptions.WorkerHeartbeatInterval {
		t.Fatalf("native client option was lost: heartbeat interval = %s", received.WorkerHeartbeatInterval)
	}
	if len(received.ContextPropagators) != 2 || received.ContextPropagators[0] != originalPropagator || received.ContextPropagators[1] == nil {
		t.Fatalf("context propagators = %#v; want application and common propagators", received.ContextPropagators)
	}
	if len(received.Interceptors) != 2 || received.Interceptors[0] != applicationInterceptor {
		t.Fatalf("client interceptors = %#v; want application interceptor and tracing interceptor", received.Interceptors)
	}
	if received.Logger == nil {
		t.Fatal("common Temporal logger was not installed")
	}
	received.Logger.Info("client connected", "service", "worker")
	if entries := observed.All(); len(entries) != 1 || entries[0].Message != "client connected" || entries[0].ContextMap()["service"] != "worker" {
		t.Fatalf("common logger received unexpected SDK log output: %#v", entries)
	}
	if propagatorBacking[:cap(propagatorBacking)][0] != mutatedPropagator || propagatorBacking[:cap(propagatorBacking)][1] != nil {
		t.Fatal("client option composition changed the caller's propagator slice")
	}
	if interceptorBacking[:cap(interceptorBacking)][0] != applicationInterceptor || interceptorBacking[:cap(interceptorBacking)][1] != nil {
		t.Fatal("client option composition changed the caller's interceptor slice")
	}
	if nativeOptions.ConnectionOptions.TLS.ServerName != "native.example" {
		t.Fatal("client option composition changed the caller's TLS config")
	}
}

func TestClientConfigurationOverridesNativeTLS(t *testing.T) {
	nativeOptions := sdkclient.Options{
		ConnectionOptions: sdkclient.ConnectionOptions{TLS: &tls.Config{ServerName: "native.example"}},
	}
	var received sdkclient.Options
	_, err := dial(context.Background(), logger.Wrap(zap.NewNop()), validTemporalSettings(), []Option{WithSDKOptions(nativeOptions)}, func(_ context.Context, options sdkclient.Options) (sdkclient.Client, error) {
		received = options
		return &testSDKClient{}, nil
	})
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	if received.ConnectionOptions.TLS != nil || !received.ConnectionOptions.TLSDisabled {
		t.Fatalf("disabled TLS settings did not override native options: %#v", received.ConnectionOptions)
	}
}

func TestClientPreservesTypedDialFailure(t *testing.T) {
	upstreamErr := &serviceerror.Unavailable{Message: "server is unavailable"}
	_, err := dial(context.Background(), logger.Wrap(zap.NewNop()), validTemporalSettings(), nil, func(context.Context, sdkclient.Options) (sdkclient.Client, error) {
		return nil, upstreamErr
	})
	var got *serviceerror.Unavailable
	if !errors.Is(err, ErrDialFailed) || !errors.As(err, &got) || got != upstreamErr {
		t.Fatalf("dial() error = %#v; want wrapped SDK error %#v", err, upstreamErr)
	}
}

func validTemporalSettings() settings.Temporal {
	return settings.Temporal{Address: "127.0.0.1:7233", Namespace: "go-common-test"}
}

type testSDKClient struct{ sdkclient.Client }

type testClientInterceptor struct {
	interceptor.ClientInterceptorBase
}

type testContextPropagator struct{}

func (*testContextPropagator) Inject(context.Context, workflow.HeaderWriter) error { return nil }

func (*testContextPropagator) Extract(ctx context.Context, _ workflow.HeaderReader) (context.Context, error) {
	return ctx, nil
}

func (*testContextPropagator) InjectFromWorkflow(workflow.Context, workflow.HeaderWriter) error {
	return nil
}

func (*testContextPropagator) ExtractToWorkflow(ctx workflow.Context, _ workflow.HeaderReader) (workflow.Context, error) {
	return ctx, nil
}

var _ workflow.ContextPropagator = (*testContextPropagator)(nil)
