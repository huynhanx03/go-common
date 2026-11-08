package etcd

import "errors"

var (
	ErrInvalidConfig  = errors.New("invalid etcd coordination configuration")
	ErrClusterChanged = errors.New("etcd cluster identity changed")
)
