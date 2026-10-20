package websocket_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/websocket"
)

func validOptions() websocket.Options {
	options := websocket.DefaultOptions()
	options.AllowedOrigins = []string{"https://app.example.com"}
	return options
}

func TestDefaultOptionsUseBoundedProductionValues(t *testing.T) {
	t.Parallel()

	options := websocket.DefaultOptions()
	if options.ReadLimit != 64<<10 {
		t.Fatalf("ReadLimit = %d, want 64 KiB", options.ReadLimit)
	}
	if options.MaxOutboundMessageBytes != 64<<10 {
		t.Fatalf("MaxOutboundMessageBytes = %d, want 64 KiB", options.MaxOutboundMessageBytes)
	}
	options.AllowedOrigins = []string{"https://app.example.com"}
	normalized, err := websocket.NormalizeOptions(options)
	if err != nil {
		t.Fatalf("NormalizeOptions: %v", err)
	}
	options.AllowedOrigins[0] = "https://mutated.example.com"
	if normalized.AllowedOrigins[0] != "https://app.example.com" {
		t.Fatal("normalized options retained caller-owned origin slice")
	}

	options = validOptions()
	options.TrustedProxies = []string{"127.0.0.1", "10.0.0.0/8"}
	normalized, err = websocket.NormalizeOptions(options)
	if err != nil {
		t.Fatalf("NormalizeOptions trusted proxies: %v", err)
	}
	options.TrustedProxies[0] = "192.0.2.1"
	if normalized.TrustedProxies[0] != "127.0.0.1" {
		t.Fatal("normalized options retained caller-owned trusted proxy slice")
	}
}

func TestOptionsRejectEveryNonPositiveBudget(t *testing.T) {
	t.Parallel()

	mutations := map[string]func(*websocket.Options){
		"read limit":                  func(options *websocket.Options) { options.ReadLimit = 0 },
		"queue capacity":              func(options *websocket.Options) { options.WriteQueueCapacity = 0 },
		"queue bytes":                 func(options *websocket.Options) { options.WriteQueueByteCapacity = 0 },
		"hub bytes":                   func(options *websocket.Options) { options.MaxHubQueuedBytes = 0 },
		"outbound bytes":              func(options *websocket.Options) { options.MaxOutboundMessageBytes = 0 },
		"inbound rate":                func(options *websocket.Options) { options.InboundMessagesPerSecond = 0 },
		"inbound burst":               func(options *websocket.Options) { options.InboundBurst = 0 },
		"connection handler slots":    func(options *websocket.Options) { options.MaxInFlightHandlersPerConnection = 0 },
		"global handler slots":        func(options *websocket.Options) { options.MaxInFlightHandlers = 0 },
		"handler timeout":             func(options *websocket.Options) { options.HandlerTimeout = 0 },
		"ping interval":               func(options *websocket.Options) { options.PingInterval = 0 },
		"pong timeout":                func(options *websocket.Options) { options.PongTimeout = 0 },
		"write timeout":               func(options *websocket.Options) { options.WriteTimeout = 0 },
		"shutdown timeout":            func(options *websocket.Options) { options.ShutdownTimeout = 0 },
		"connections":                 func(options *websocket.Options) { options.MaxConnections = 0 },
		"connections per principal":   func(options *websocket.Options) { options.MaxConnectionsPerPrincipal = 0 },
		"connections per remote IP":   func(options *websocket.Options) { options.MaxConnectionsPerRemoteIP = 0 },
		"upgrade rate per remote IP":  func(options *websocket.Options) { options.UpgradeRatePerSecondPerRemoteIP = 0 },
		"upgrade burst per remote IP": func(options *websocket.Options) { options.UpgradeBurstPerRemoteIP = 0 },
		"subscriptions":               func(options *websocket.Options) { options.MaxSubscriptions = 0 },
	}
	for name, mutate := range mutations {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			options := validOptions()
			mutate(&options)
			if _, err := websocket.NormalizeOptions(options); !errors.Is(
				err,
				websocket.ErrInvalidOptions,
			) {
				t.Fatalf("NormalizeOptions error = %v", err)
			}

			options = validOptions()
			mutate(&options)
			switch name {
			case "handler timeout":
				options.HandlerTimeout = -time.Second
			case "ping interval":
				options.PingInterval = -time.Second
			case "pong timeout":
				options.PongTimeout = -time.Second
			case "write timeout":
				options.WriteTimeout = -time.Second
			case "shutdown timeout":
				options.ShutdownTimeout = -time.Second
			default:
				setNegativeBudget(name, &options)
			}
			if _, err := websocket.NormalizeOptions(options); !errors.Is(
				err,
				websocket.ErrInvalidOptions,
			) {
				t.Fatalf("negative NormalizeOptions error = %v", err)
			}
		})
	}
}

func setNegativeBudget(name string, options *websocket.Options) {
	switch name {
	case "read limit":
		options.ReadLimit = -1
	case "queue capacity":
		options.WriteQueueCapacity = -1
	case "queue bytes":
		options.WriteQueueByteCapacity = -1
	case "hub bytes":
		options.MaxHubQueuedBytes = -1
	case "outbound bytes":
		options.MaxOutboundMessageBytes = -1
	case "inbound rate":
		options.InboundMessagesPerSecond = -1
	case "inbound burst":
		options.InboundBurst = -1
	case "connection handler slots":
		options.MaxInFlightHandlersPerConnection = -1
	case "global handler slots":
		options.MaxInFlightHandlers = -1
	case "connections":
		options.MaxConnections = -1
	case "connections per principal":
		options.MaxConnectionsPerPrincipal = -1
	case "connections per remote IP":
		options.MaxConnectionsPerRemoteIP = -1
	case "upgrade rate per remote IP":
		options.UpgradeRatePerSecondPerRemoteIP = -1
	case "upgrade burst per remote IP":
		options.UpgradeBurstPerRemoteIP = -1
	case "subscriptions":
		options.MaxSubscriptions = -1
	}
}

func TestOptionsRejectInconsistentBudgetsAndOrigins(t *testing.T) {
	t.Parallel()

	mutations := []func(*websocket.Options){
		func(options *websocket.Options) { options.AllowedOrigins = nil },
		func(options *websocket.Options) { options.AllowedOrigins = []string{"*"} },
		func(options *websocket.Options) { options.AllowedOrigins = []string{"https://app.example.com/path"} },
		func(options *websocket.Options) {
			options.AllowedOrigins = []string{"https://app.example.com", "https://app.example.com"}
		},
		func(options *websocket.Options) { options.MaxOutboundMessageBytes = options.WriteQueueByteCapacity + 1 },
		func(options *websocket.Options) { options.WriteQueueByteCapacity = options.MaxHubQueuedBytes + 1 },
		func(options *websocket.Options) {
			options.MaxInFlightHandlersPerConnection = options.MaxInFlightHandlers + 1
		},
		func(options *websocket.Options) { options.MaxConnectionsPerPrincipal = options.MaxConnections + 1 },
		func(options *websocket.Options) { options.MaxConnectionsPerRemoteIP = options.MaxConnections + 1 },
		func(options *websocket.Options) { options.InboundBurst = options.InboundMessagesPerSecond - 1 },
		func(options *websocket.Options) {
			options.UpgradeBurstPerRemoteIP = options.UpgradeRatePerSecondPerRemoteIP - 1
		},
		func(options *websocket.Options) { options.PingInterval = options.PongTimeout },
		func(options *websocket.Options) { options.ReadLimit = 2 << 20 },
		func(options *websocket.Options) { options.MaxHubQueuedBytes = 2 << 30 },
		func(options *websocket.Options) { options.TrustedProxies = []string{"not-an-ip"} },
		func(options *websocket.Options) {
			options.TrustedProxies = []string{"127.0.0.1", "127.0.0.1/32"}
		},
	}
	for index, mutate := range mutations {
		options := validOptions()
		mutate(&options)
		if _, err := websocket.NormalizeOptions(options); !errors.Is(
			err,
			websocket.ErrInvalidOptions,
		) {
			t.Fatalf("mutation[%d] error = %v", index, err)
		}
	}

	tooManyOrigins := validOptions()
	tooManyOrigins.AllowedOrigins = make([]string, 33)
	for index := range tooManyOrigins.AllowedOrigins {
		tooManyOrigins.AllowedOrigins[index] = "https://" + strings.Repeat("a", index+1) + ".example.com"
	}
	if _, err := websocket.NormalizeOptions(tooManyOrigins); !errors.Is(
		err,
		websocket.ErrInvalidOptions,
	) {
		t.Fatalf("too many origins error = %v", err)
	}
}
