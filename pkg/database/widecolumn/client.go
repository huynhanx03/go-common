package widecolumn

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gocql/gocql"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	defaultPort    = 9042
	defaultTimeout = 10
	defaultRetries = 3
	maxTimeout     = 10 * 60
	maxRetries     = 10
	maxHosts       = 64
)

// WideColumnClient defines the interface for Wide Column DB client operations.
type WideColumnClient interface {
	Connect() error
	Close()
	GetSession() *gocql.Session
}

var _ WideColumnClient = (*Client)(nil)

// Pinger is implemented by clients that expose a readiness probe.
type Pinger interface {
	Ping(context.Context) error
}

var _ Pinger = (*Client)(nil)

// NewClient creates a new Client instance. It preserves the legacy one-result
// constructor; invalid configuration is retained and returned by Connect.
// NewClientChecked is available for applications that fail fast during boot.
func NewClient(cfg *settings.WideColumn) *Client {
	client := &Client{}
	client.setConfig(cfg)
	return client
}

// NewClientChecked validates and clones cfg immediately.
func NewClientChecked(cfg *settings.WideColumn) (*Client, error) {
	client := NewClient(cfg)
	if err := client.ValidationError(); err != nil {
		return nil, err
	}
	return client, nil
}

// Client represents a Wide Column DB connection.
type Client struct {
	// Session is retained for source compatibility. New code should use
	// GetSession so reconnect/close operations can be synchronized safely.
	Session *gocql.Session

	mu        sync.RWMutex
	connectMu sync.Mutex
	config    *settings.WideColumn
	initErr   error
}

// ValidationError reports a configuration error retained by NewClient.
func (c *Client) ValidationError() error {
	if c == nil {
		return ErrUninitializedClient
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.initErr
}

// Connect creates a session. A reconnect creates the replacement first, then
// atomically swaps it in and closes the old session, so a failed reconnect
// never destroys a healthy existing connection.
func (c *Client) Connect() error {
	if c == nil {
		return ErrUninitializedClient
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	c.mu.RLock()
	configErr := c.initErr
	var config settings.WideColumn
	if c.config != nil {
		config = cloneWideColumnConfig(*c.config)
	}
	c.mu.RUnlock()
	if configErr != nil {
		return configErr
	}
	normalized, err := normalizeWideColumnConfig(&config)
	if err != nil {
		c.setInitError(err)
		return err
	}

	cluster := gocql.NewCluster(normalized.Hosts...)
	cluster.Port = normalized.Port
	cluster.Keyspace = normalized.Keyspace
	cluster.Authenticator = gocql.PasswordAuthenticator{
		Username: normalized.Username,
		Password: normalized.Password,
	}
	cluster.Timeout = time.Duration(normalized.Timeout) * time.Second
	cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: normalized.Retries}
	cluster.Consistency = gocql.Quorum

	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConnectFailed, err)
	}

	c.mu.Lock()
	oldSession := c.Session
	c.Session = session
	c.config = &normalized
	c.initErr = nil
	c.mu.Unlock()
	if oldSession != nil && oldSession != session {
		oldSession.Close()
	}
	return nil
}

// setDefaultConfig retains the old internal helper name while making the
// operation non-mutating to the caller's configuration object.
func (c *Client) setDefaultConfig() {
	if c == nil {
		return
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	c.mu.RLock()
	var source *settings.WideColumn
	if c.config != nil {
		clone := cloneWideColumnConfig(*c.config)
		source = &clone
	}
	c.mu.RUnlock()
	normalized, err := normalizeWideColumnConfig(source)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.initErr = err
		return
	}
	c.config = &normalized
	c.initErr = nil
}

// setConfig clones and validates a caller-owned configuration.
func (c *Client) setConfig(cfg *settings.WideColumn) {
	if c == nil {
		return
	}
	normalized, err := normalizeWideColumnConfig(cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.config = nil
		c.initErr = err
		return
	}
	c.config = &normalized
	c.initErr = nil
}

func (c *Client) setInitError(err error) {
	c.mu.Lock()
	c.initErr = err
	c.mu.Unlock()
}

// Close closes the current session and is safe to call repeatedly or on a nil
// receiver. The pointer is cleared before closing so a concurrent reconnect
// cannot expose the old session as current.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	c.mu.Lock()
	session := c.Session
	c.Session = nil
	c.mu.Unlock()
	if session != nil {
		session.Close()
	}
}

// GetSession returns the current session, or nil when the client is not
// connected. The returned gocql session remains owned by Client.
func (c *Client) GetSession() *gocql.Session {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Session
}

// Ping verifies that the current session can execute a lightweight
// keyspace-independent system query. It never logs credentials or paging
// state and is suitable for readiness/health checks.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return ErrUninitializedClient
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	session := c.GetSession()
	if session == nil {
		return ErrNotConnected
	}
	var release string
	if err := session.Query("SELECT release_version FROM system.local").WithContext(ctx).Scan(&release); err != nil {
		return fmt.Errorf("%w: %w", ErrPingFailed, err)
	}
	return nil
}

func normalizeWideColumnConfig(cfg *settings.WideColumn) (settings.WideColumn, error) {
	if cfg == nil {
		return settings.WideColumn{}, fmt.Errorf("%w: %w", ErrInvalidConfiguration, ErrNilConfiguration)
	}
	result := cloneWideColumnConfig(*cfg)
	if len(result.Hosts) == 0 || len(result.Hosts) > maxHosts {
		return settings.WideColumn{}, fmt.Errorf(
			"%w: hosts must contain between 1 and %d entries",
			ErrInvalidConfiguration,
			maxHosts,
		)
	}
	seenHosts := make(map[string]struct{}, len(result.Hosts))
	for index, host := range result.Hosts {
		if host == "" || host != strings.TrimSpace(host) || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
			return settings.WideColumn{}, fmt.Errorf("%w: invalid host at index %d", ErrInvalidConfiguration, index)
		}
		canonicalHost := strings.ToLower(host)
		if _, exists := seenHosts[canonicalHost]; exists {
			return settings.WideColumn{}, fmt.Errorf("%w: duplicate host %q", ErrInvalidConfiguration, host)
		}
		seenHosts[canonicalHost] = struct{}{}
	}
	if result.Port == 0 {
		result.Port = defaultPort
	}
	if result.Port < 1 || result.Port > 65535 {
		return settings.WideColumn{}, fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidConfiguration)
	}
	if result.Timeout == 0 {
		result.Timeout = defaultTimeout
	}
	if result.Timeout < 1 || result.Timeout > maxTimeout {
		return settings.WideColumn{}, fmt.Errorf(
			"%w: timeout must be between 1 and %d seconds",
			ErrInvalidConfiguration,
			maxTimeout,
		)
	}
	if result.Retries == 0 {
		result.Retries = defaultRetries
	}
	if result.Retries < 0 || result.Retries > maxRetries {
		return settings.WideColumn{}, fmt.Errorf(
			"%w: retries must be between 0 and %d",
			ErrInvalidConfiguration,
			maxRetries,
		)
	}
	if result.Keyspace != "" {
		keyspace, err := normalizeIdentifier(result.Keyspace)
		if err != nil {
			return settings.WideColumn{}, fmt.Errorf("%w: invalid keyspace", ErrInvalidConfiguration)
		}
		result.Keyspace = keyspace
	}
	return result, nil
}

func cloneWideColumnConfig(cfg settings.WideColumn) settings.WideColumn {
	cfg.Hosts = append([]string(nil), cfg.Hosts...)
	return cfg
}
