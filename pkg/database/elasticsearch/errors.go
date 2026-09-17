package elasticsearch

import "errors"

var (
	// Client errors
	ErrCreateClientFailed = errors.New("failed to create elasticsearch client")
	ErrConnectFailed      = errors.New("failed to connect to elasticsearch")
	ErrInfoRequestFailed  = errors.New("elasticsearch info request failed")

	// Repository errors
	ErrMarshalFailed       = errors.New("failed to marshal document")
	ErrIndexRequestFailed  = errors.New("failed to execute index request")
	ErrGetRequestFailed    = errors.New("failed to execute get request")
	ErrDeleteRequestFailed = errors.New("failed to execute delete request")
	ErrSearchRequestFailed = errors.New("failed to execute search request")
	ErrDecodeFailed        = errors.New("failed to decode response")

	ErrInvalidConfig      = errors.New("invalid elasticsearch configuration")
	ErrInvalidRepository  = errors.New("invalid elasticsearch repository")
	ErrInvalidQuery       = errors.New("invalid elasticsearch query")
	ErrInvalidIdentifier  = errors.New("invalid elasticsearch field identifier")
	ErrInvalidID          = errors.New("invalid elasticsearch document id")
	ErrInvalidContext     = errors.New("invalid elasticsearch context")
	ErrInvalidCursor      = errors.New("invalid elasticsearch cursor")
	ErrBatchTooLarge      = errors.New("elasticsearch batch too large")
	ErrDocumentTooLarge   = errors.New("elasticsearch document too large")
	ErrResponseTooLarge   = errors.New("elasticsearch response too large")
	ErrBulkPartialFailure = errors.New("elasticsearch bulk request partially failed")
)
