package cache

import "github.com/pkg/errors"

var (
	ErrInvalidConfig = errors.New("invalid cache configuration")
	ErrKeyNotFound   = errors.New("key not found")
	ErrNotFound      = errors.New("entity not found")
	errInvalidEntry  = errors.New("cache: invalid entry")
)
