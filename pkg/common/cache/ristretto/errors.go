package ristretto

import "errors"

var (
	errCacheClosed            = errors.New("cache is closed")
	errVersionWriteRejected   = errors.New("cache version write rejected")
	errConditionalWriteFailed = errors.New("conditional cache write rejected")
)
