package ristretto

import "errors"

var (
	errCacheClosed             = errors.New("cache is closed")
	errGenerationWriteRejected = errors.New("cache generation write rejected")
	errConditionalWriteFailed  = errors.New("conditional cache write rejected")
)
