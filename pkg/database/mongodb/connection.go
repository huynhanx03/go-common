package mongodb

import (
	"github.com/huynhanx03/go-common/pkg/settings"
)

// New creates a new MongoDB connection
func New(config *settings.MongoDB) (*Client, error) {
	client := &Client{
		config: config,
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	return client, nil
}
