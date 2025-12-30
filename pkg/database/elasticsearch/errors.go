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
)
