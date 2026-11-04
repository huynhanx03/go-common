package toggle

// ClientOption customizes runtime behavior that should not live in serialized settings.
type ClientOption func(*clientOptions)

type clientOptions struct {
	onError func(error)
}

// WithOnError registers a callback for background source and SDK errors.
// The callback should be quick, nonblocking, nonpanicking, and must not call
// Close recursively.
func WithOnError(callback func(error)) ClientOption {
	return func(options *clientOptions) {
		options.onError = callback
	}
}

func resolveClientOptions(options []ClientOption) clientOptions {
	var resolved clientOptions
	for _, option := range options {
		if option != nil {
			option(&resolved)
		}
	}
	return resolved
}
