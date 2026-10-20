package widecolumn

import "github.com/huynhanx03/go-common/pkg/settings"

// New creates a new Wide Column DB connection
func New(config *settings.WideColumn) (*Client, error) {
	client, err := NewClientChecked(config)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}

	return client, nil
}
