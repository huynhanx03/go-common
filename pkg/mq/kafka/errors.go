package kafka

import "errors"

var ErrConsumerStarted = errors.New("consumer group has already started")
