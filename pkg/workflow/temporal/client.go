package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

// Option configures optional Temporal SDK features.
type Option func(*dialOptions)

type dialOptions struct {
	sdkOptions     client.Options
	tracerProvider trace.TracerProvider
}

// Client owns a connected Temporal SDK client.
type Client struct {
	sdk       client.Client
	closeOnce sync.Once
}

// Dial connects to Temporal using the caller's context and application settings.
func Dial(ctx context.Context, log *logger.Logger, config settings.Temporal, options ...Option) (*Client, error) {
	return dial(ctx, log, config, options, client.DialContext)
}

// WithSDKOptions adds native SDK features that go-common does not wrap.
func WithSDKOptions(options client.Options) Option {
	owned := cloneClientOptions(options)
	return func(config *dialOptions) {
		config.sdkOptions = cloneClientOptions(owned)
	}
}

// WithTracing enables the official Temporal OpenTelemetry interceptor.
func WithTracing(provider trace.TracerProvider) Option {
	return func(config *dialOptions) {
		config.tracerProvider = provider
	}
}

func dial(
	ctx context.Context,
	log *logger.Logger,
	config settings.Temporal,
	options []Option,
	dialClient func(context.Context, client.Options) (client.Client, error),
) (*Client, error) {
	if err := validateClientConfig(config); err != nil {
		return nil, err
	}

	optionValues := dialOptions{}
	for _, option := range options {
		option(&optionValues)
	}

	sdkOptions := cloneClientOptions(optionValues.sdkOptions)
	sdkOptions.HostPort = config.Address
	sdkOptions.Namespace = config.Namespace
	if config.TLSEnabled {
		tlsConfig, err := loadTLSConfig(config)
		if err != nil {
			return nil, err
		}
		sdkOptions.ConnectionOptions.TLS = tlsConfig
		sdkOptions.ConnectionOptions.TLSDisabled = false
	} else {
		sdkOptions.ConnectionOptions.TLS = nil
		sdkOptions.ConnectionOptions.TLSDisabled = true
	}
	if config.APIKey != "" {
		sdkOptions.Credentials = client.NewAPIKeyStaticCredentials(config.APIKey)
	}
	sdkOptions.Logger = newSDKLogger(log)
	for _, propagator := range sdkOptions.ContextPropagators {
		if _, ok := propagator.(*contextPropagator); ok {
			return nil, ErrDuplicateContextPropagator
		}
	}
	sdkOptions.ContextPropagators = append(sdkOptions.ContextPropagators, &contextPropagator{})
	if optionValues.tracerProvider != nil {
		tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
			Tracer: optionValues.tracerProvider.Tracer(tracerName),
		})
		if err != nil {
			return nil, fmt.Errorf("configure Temporal tracing: %w", err)
		}
		sdkOptions.Interceptors = append(sdkOptions.Interceptors, tracingInterceptor)
	}

	if config.DialTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.DialTimeoutSeconds)*time.Second)
		defer cancel()
	}

	sdkClient, err := dialClient(ctx, sdkOptions)
	if err != nil {
		connectionErr := fmt.Errorf("connect to Temporal: %w", err)
		if ctx.Err() != nil {
			return nil, errors.Join(ErrDialFailed, ctx.Err(), connectionErr)
		}
		return nil, errors.Join(ErrDialFailed, connectionErr)
	}
	return &Client{sdk: sdkClient}, nil
}

func validateClientConfig(config settings.Temporal) error {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Namespace) == "" ||
		config.DialTimeoutSeconds < 0 || config.DialTimeoutSeconds > int(maximumTemporalTimeout/time.Second) {
		return ErrInvalidConfig
	}
	if (config.APIKey != "" || config.ServerName != "" || config.CAFile != "" || config.CertFile != "" || config.KeyFile != "") && !config.TLSEnabled {
		return ErrInvalidConfig
	}
	if (config.CertFile == "") != (config.KeyFile == "") {
		return ErrInvalidConfig
	}
	return nil
}

func loadTLSConfig(config settings.Temporal) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: config.ServerName}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, errors.Join(ErrInvalidTLSConfig, fmt.Errorf("read CA file: %w", err))
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, ErrInvalidTLSConfig
		}
		tlsConfig.RootCAs = roots
	}
	if config.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, errors.Join(ErrInvalidTLSConfig, fmt.Errorf("load client certificate: %w", err))
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}

func cloneClientOptions(options client.Options) client.Options {
	options.ContextPropagators = append([]workflow.ContextPropagator(nil), options.ContextPropagators...)
	options.Interceptors = append([]interceptor.ClientInterceptor(nil), options.Interceptors...)
	options.Plugins = append([]client.Plugin(nil), options.Plugins...)
	options.ConnectionOptions.DialOptions = append([]grpc.DialOption(nil), options.ConnectionOptions.DialOptions...)
	if options.ConnectionOptions.TLS != nil {
		options.ConnectionOptions.TLS = options.ConnectionOptions.TLS.Clone()
	}
	return options
}

// SDK returns the native Temporal client for schedules, workflow starts, and signals.
func (c *Client) SDK() client.Client { return c.sdk }

// Close releases the connection once.
func (c *Client) Close() { c.closeOnce.Do(c.sdk.Close) }
