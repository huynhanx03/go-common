package widecolumn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/huynhanx03/go-common/pkg/settings"
)

func wideColumnValidClientConfig() *settings.WideColumn {
	return &settings.WideColumn{
		Hosts:    []string{"node-a", "node-b"},
		Keyspace: "Events",
		Port:     0,
		Timeout:  0,
		Retries:  0,
	}
}

func TestNewClientClonesAndNormalizesConfig(t *testing.T) {
	config := wideColumnValidClientConfig()
	client, err := NewClientChecked(config)
	require.NoError(t, err)

	config.Hosts[0] = "caller-mutated"
	config.Hosts = append(config.Hosts, "caller-added")
	config.Keyspace = "caller-mutated"

	client.mu.RLock()
	defer client.mu.RUnlock()
	assert.Equal(t, []string{"node-a", "node-b"}, client.config.Hosts)
	assert.Equal(t, "events", client.config.Keyspace)
	assert.Equal(t, defaultPort, client.config.Port)
	assert.Equal(t, defaultTimeout, client.config.Timeout)
	assert.Equal(t, defaultRetries, client.config.Retries)
}

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config *settings.WideColumn
	}{
		{name: "nil config", config: nil},
		{name: "empty hosts", config: &settings.WideColumn{}},
		{name: "empty host", config: &settings.WideColumn{Hosts: []string{""}}},
		{name: "host whitespace", config: &settings.WideColumn{Hosts: []string{" node-a"}}},
		{name: "duplicate hosts", config: &settings.WideColumn{Hosts: []string{"node-a", "node-a"}}},
		{name: "invalid port", config: &settings.WideColumn{Hosts: []string{"node-a"}, Port: 65536}},
		{name: "negative timeout", config: &settings.WideColumn{Hosts: []string{"node-a"}, Timeout: -1}},
		{name: "huge timeout", config: &settings.WideColumn{Hosts: []string{"node-a"}, Timeout: maxTimeout + 1}},
		{name: "negative retries", config: &settings.WideColumn{Hosts: []string{"node-a"}, Retries: -1}},
		{name: "huge retries", config: &settings.WideColumn{Hosts: []string{"node-a"}, Retries: maxRetries + 1}},
		{name: "unsafe keyspace", config: &settings.WideColumn{Hosts: []string{"node-a"}, Keyspace: "events;drop"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClientChecked(test.config)
			assert.Error(t, err)
			assert.Nil(t, client)
			assert.True(t, errors.Is(err, ErrInvalidConfiguration) || errors.Is(err, ErrNilConfiguration))
		})
	}
}

func TestClientZeroValueAndCloseAreSafe(t *testing.T) {
	var client *Client
	assert.ErrorIs(t, client.Connect(), ErrUninitializedClient)
	assert.ErrorIs(t, client.ValidationError(), ErrUninitializedClient)
	assert.Nil(t, client.GetSession())
	assert.ErrorIs(t, client.Ping(context.Background()), ErrUninitializedClient)
	assert.ErrorIs(t, client.Ping(nil), ErrUninitializedClient)
	client.Close()

	zero := &Client{}
	assert.Error(t, zero.Connect())
	assert.ErrorIs(t, zero.Ping(nil), ErrInvalidContext)
	assert.ErrorIs(t, zero.Ping(context.Background()), ErrNotConnected)
	zero.Close()
	zero.Close()
}

func TestClientSourceDoesNotMutateCallerOrSwapBeforeConnectSucceeds(t *testing.T) {
	source, err := os.ReadFile("client.go")
	require.NoError(t, err)
	contents := string(source)

	assert.Contains(t, contents, "cloneWideColumnConfig")
	assert.Contains(t, contents, "oldSession := c.Session")
	assert.Contains(t, contents, "if oldSession != nil && oldSession != session")
	assert.Contains(t, contents, "c.Session = nil")
	assert.Contains(t, contents, "sync.RWMutex")
	assert.NotContains(t, contents, "c.config.Port = defaultPort")
	assert.NotContains(t, contents, "c.config.Timeout = defaultTimeout")
	assert.NotContains(t, contents, "c.config.Retries = defaultRetries")
}

func TestNormalizeWideColumnConfigRejectsOversizedHostList(t *testing.T) {
	hosts := make([]string, maxHosts+1)
	for index := range hosts {
		hosts[index] = "node-" + strings.Repeat("a", index%3+1)
	}
	_, err := normalizeWideColumnConfig(&settings.WideColumn{Hosts: hosts})
	assert.ErrorIs(t, err, ErrInvalidConfiguration)
}
