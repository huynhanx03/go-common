package widecolumn

import (
	"fmt"
	"time"

	"github.com/gocql/gocql"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	defaultPort    = 9042
	defaultTimeout = 10
	defaultRetries = 3
)

// WideColumnClient defines the interface for Wide Column DB client operations
type WideColumnClient interface {
	Connect() error
	Close()
	GetSession() *gocql.Session
}

var _ WideColumnClient = (*Client)(nil)

// NewClient creates a new Client instance
func NewClient(cfg *settings.WideColumn) *Client {
	return &Client{
		config: cfg,
	}
}

// Client represents a Wide Column DB connection
type Client struct {
	Session *gocql.Session
	config  *settings.WideColumn
}

// Connect creates a new Wide Column DB connection
func (c *Client) Connect() error {
	c.setDefaultConfig()

	cluster := gocql.NewCluster(c.config.Hosts...)
	cluster.Port = c.config.Port
	cluster.Keyspace = c.config.Keyspace
	cluster.Authenticator = gocql.PasswordAuthenticator{
		Username: c.config.Username,
		Password: c.config.Password,
	}
	cluster.Timeout = time.Duration(c.config.Timeout) * time.Second
	cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: c.config.Retries}
	cluster.Consistency = gocql.Quorum

	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnectFailed, err)
	}

	c.Session = session
	return nil
}

func (c *Client) setDefaultConfig() {
	if c.config.Port == 0 {
		c.config.Port = defaultPort
	}
	if c.config.Timeout == 0 {
		c.config.Timeout = defaultTimeout
	}
	if c.config.Retries == 0 {
		c.config.Retries = defaultRetries
	}
}

// Close closes the database connection
func (c *Client) Close() {
	if c.Session != nil {
		c.Session.Close()
	}
}

// GetSession returns the underlying gocql session
func (c *Client) GetSession() *gocql.Session {
	return c.Session
}
