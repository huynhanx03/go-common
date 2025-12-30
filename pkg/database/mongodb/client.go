package mongodb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/utils"
)

const (
	defaultTimeout         = 10
	defaultMaxPoolSize     = 100
	defaultMinPoolSize     = 10
	defaultMaxConnIdleTime = 60
)

// MongoDBClient defines the interface for MongoDB client operations
type MongoDBClient interface {
	connect() error
	setDefaultConfig()
	buildURI() string
	Close() error
}

var _ MongoDBClient = (*Client)(nil)

// Client represents a MongoDB connection
type Client struct {
	Client *mongo.Client
	DB     *mongo.Database
	config *settings.MongoDB
}

// connect creates a new MongoDB connection
func (c *Client) connect() error {
	c.setDefaultConfig()

	ctx, cancel := context.WithTimeout(context.Background(), utils.ToDuration(c.config.Timeout))
	defer cancel()

	uri := c.buildURI()

	clientOpts := options.Client().
		ApplyURI(uri).
		SetMaxPoolSize(c.config.MaxPoolSize).
		SetMinPoolSize(c.config.MinPoolSize).
		SetMaxConnIdleTime(utils.ToDuration(c.config.MaxConnIdleTime)).
		SetConnectTimeout(utils.ToDuration(c.config.Timeout))

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnectFailed, err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("%w: %v", ErrPingFailed, err)
	}

	c.Client = client
	c.DB = client.Database(c.config.Database)

	return nil
}

func (c *Client) setDefaultConfig() {
	if c.config.Timeout == 0 {
		c.config.Timeout = defaultTimeout
	}
	if c.config.MaxPoolSize == 0 {
		c.config.MaxPoolSize = defaultMaxPoolSize
	}
	if c.config.MinPoolSize == 0 {
		c.config.MinPoolSize = defaultMinPoolSize
	}
	if c.config.MaxConnIdleTime == 0 {
		c.config.MaxConnIdleTime = defaultMaxConnIdleTime
	}
}

func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Client.Disconnect(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDisconnectFailed, err)
	}
	return nil
}

func (c *Client) buildURI() string {
	if c.config.Username != "" && c.config.Password != "" {
		return fmt.Sprintf(
			"mongodb://%s@%s:%d/?directConnection=true",
			url.UserPassword(c.config.Username, c.config.Password).String(),
			c.config.Host,
			c.config.Port,
		)
	}

	return fmt.Sprintf("mongodb://%s:%d/?directConnection=true", c.config.Host, c.config.Port)
}
