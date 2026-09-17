package websocket

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMessageBytes = 64 << 10
	HardMaxMessageBytes = 1 << 20

	maxAllowedOrigins       = 32
	maxTrustedProxies       = 32
	maxOriginBytes          = 2048
	maxWriteQueueCapacity   = 4096
	maxWriteQueueBytes      = 64 << 20
	maxHubQueuedBytes       = 1 << 30
	maxMessagesPerSecond    = 10_000
	maxInFlightHandlers     = 100_000
	maxHandlerTimeout       = 10 * time.Minute
	maxPingInterval         = 10 * time.Minute
	maxPongTimeout          = 2 * time.Minute
	maxWriteTimeout         = 2 * time.Minute
	maxShutdownTimeout      = 10 * time.Minute
	maxConnections          = 1_000_000
	maxUpgradeRatePerSecond = 10_000
	maxSubscriptions        = 1024
)

type Options struct {
	AllowedOrigins                   []string
	TrustedProxies                   []string
	ReadLimit                        int64
	WriteQueueCapacity               int
	WriteQueueByteCapacity           int64
	MaxHubQueuedBytes                int64
	MaxOutboundMessageBytes          int64
	InboundMessagesPerSecond         int
	InboundBurst                     int
	MaxInFlightHandlersPerConnection int
	MaxInFlightHandlers              int
	HandlerTimeout                   time.Duration
	PingInterval                     time.Duration
	PongTimeout                      time.Duration
	WriteTimeout                     time.Duration
	ShutdownTimeout                  time.Duration
	MaxConnections                   int
	MaxConnectionsPerPrincipal       int
	MaxConnectionsPerRemoteIP        int
	UpgradeRatePerSecondPerRemoteIP  int
	UpgradeBurstPerRemoteIP          int
	MaxSubscriptions                 int
}

// DefaultOptions returns bounded production defaults. AllowedOrigins remains
// empty intentionally; bootstrap must provide the exact deployment origins.
func DefaultOptions() Options {
	return Options{
		ReadLimit:                        DefaultMessageBytes,
		WriteQueueCapacity:               128,
		WriteQueueByteCapacity:           8 << 20,
		MaxHubQueuedBytes:                256 << 20,
		MaxOutboundMessageBytes:          DefaultMessageBytes,
		InboundMessagesPerSecond:         32,
		InboundBurst:                     64,
		MaxInFlightHandlersPerConnection: 4,
		MaxInFlightHandlers:              256,
		HandlerTimeout:                   15 * time.Second,
		PingInterval:                     30 * time.Second,
		PongTimeout:                      10 * time.Second,
		WriteTimeout:                     5 * time.Second,
		ShutdownTimeout:                  20 * time.Second,
		MaxConnections:                   10_000,
		MaxConnectionsPerPrincipal:       8,
		MaxConnectionsPerRemoteIP:        100,
		UpgradeRatePerSecondPerRemoteIP:  5,
		UpgradeBurstPerRemoteIP:          20,
		MaxSubscriptions:                 64,
	}
}

// NormalizeOptions validates every allocation, concurrency, rate, and
// lifecycle budget and returns an immutable copy of caller-owned slices.
func NormalizeOptions(input Options) (Options, error) {
	if err := validateOptions(input); err != nil {
		return Options{}, err
	}
	input.AllowedOrigins, _ = normalizeOrigins(input.AllowedOrigins)
	input.TrustedProxies = append([]string(nil), input.TrustedProxies...)
	return input, nil
}

func validateOptions(options Options) error {
	if err := validateOrigins(options.AllowedOrigins); err != nil {
		return err
	}
	if _, err := parseTrustedProxies(options.TrustedProxies); err != nil {
		return err
	}
	if options.ReadLimit <= 0 ||
		options.ReadLimit > HardMaxMessageBytes ||
		options.WriteQueueCapacity <= 0 ||
		options.WriteQueueCapacity > maxWriteQueueCapacity ||
		options.WriteQueueByteCapacity <= 0 ||
		options.WriteQueueByteCapacity > maxWriteQueueBytes ||
		options.MaxHubQueuedBytes <= 0 ||
		options.MaxHubQueuedBytes > maxHubQueuedBytes ||
		options.MaxOutboundMessageBytes <= 0 ||
		options.MaxOutboundMessageBytes > HardMaxMessageBytes ||
		options.InboundMessagesPerSecond <= 0 ||
		options.InboundMessagesPerSecond > maxMessagesPerSecond ||
		options.InboundBurst <= 0 ||
		options.InboundBurst > maxMessagesPerSecond ||
		options.MaxInFlightHandlersPerConnection <= 0 ||
		options.MaxInFlightHandlersPerConnection > maxInFlightHandlers ||
		options.MaxInFlightHandlers <= 0 ||
		options.MaxInFlightHandlers > maxInFlightHandlers ||
		options.HandlerTimeout <= 0 ||
		options.HandlerTimeout > maxHandlerTimeout ||
		options.PingInterval <= 0 ||
		options.PingInterval > maxPingInterval ||
		options.PongTimeout <= 0 ||
		options.PongTimeout > maxPongTimeout ||
		options.WriteTimeout <= 0 ||
		options.WriteTimeout > maxWriteTimeout ||
		options.ShutdownTimeout <= 0 ||
		options.ShutdownTimeout > maxShutdownTimeout ||
		options.MaxConnections <= 0 ||
		options.MaxConnections > maxConnections ||
		options.MaxConnectionsPerPrincipal <= 0 ||
		options.MaxConnectionsPerPrincipal > maxConnections ||
		options.MaxConnectionsPerRemoteIP <= 0 ||
		options.MaxConnectionsPerRemoteIP > maxConnections ||
		options.UpgradeRatePerSecondPerRemoteIP <= 0 ||
		options.UpgradeRatePerSecondPerRemoteIP > maxUpgradeRatePerSecond ||
		options.UpgradeBurstPerRemoteIP <= 0 ||
		options.UpgradeBurstPerRemoteIP > maxUpgradeRatePerSecond ||
		options.MaxSubscriptions <= 0 ||
		options.MaxSubscriptions > maxSubscriptions {
		return ErrInvalidOptions
	}
	if options.MaxOutboundMessageBytes > options.WriteQueueByteCapacity ||
		options.WriteQueueByteCapacity > options.MaxHubQueuedBytes ||
		options.MaxInFlightHandlersPerConnection > options.MaxInFlightHandlers ||
		options.MaxConnectionsPerPrincipal > options.MaxConnections ||
		options.MaxConnectionsPerRemoteIP > options.MaxConnections ||
		options.InboundBurst < options.InboundMessagesPerSecond ||
		options.UpgradeBurstPerRemoteIP < options.UpgradeRatePerSecondPerRemoteIP ||
		options.PingInterval <= options.PongTimeout {
		return ErrInvalidOptions
	}
	return nil
}

func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	if len(values) > maxTrustedProxies {
		return nil, ErrInvalidOptions
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, ErrInvalidOptions
		}
		var prefix netip.Prefix
		if parsed, err := netip.ParsePrefix(value); err == nil {
			prefix = parsed.Masked()
		} else {
			address, addressError := netip.ParseAddr(value)
			if addressError != nil {
				return nil, ErrInvalidOptions
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if _, duplicate := seen[prefix]; duplicate {
			return nil, ErrInvalidOptions
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func validateOrigins(origins []string) error {
	_, err := normalizeOrigins(origins)
	return err
}

func normalizeOrigins(origins []string) ([]string, error) {
	if len(origins) == 0 || len(origins) > maxAllowedOrigins {
		return nil, ErrInvalidOptions
	}
	normalized := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, raw := range origins {
		origin, err := normalizeOrigin(raw)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, ErrInvalidOptions
		}
		seen[origin] = struct{}{}
		normalized = append(normalized, origin)
	}
	return normalized, nil
}

func normalizeOrigin(origin string) (string, error) {
	if origin == "" ||
		len(origin) > maxOriginBytes ||
		strings.TrimSpace(origin) != origin ||
		origin == "*" {
		return "", ErrInvalidOptions
	}
	for index := 0; index < len(origin); index++ {
		if origin[index] <= 0x20 || origin[index] >= 0x7f {
			return "", ErrInvalidOptions
		}
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed == nil {
		return "", ErrInvalidOptions
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "https" && scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Path != "" ||
		parsed.RawPath != "" ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return "", ErrInvalidOptions
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.Contains(host, "%") {
		return "", ErrInvalidOptions
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", ErrInvalidOptions
		}
		port = strconv.FormatUint(portNumber, 10)
		if (scheme == "http" && port == "80") ||
			(scheme == "https" && port == "443") {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, nil
}
