package outbox

import "errors"

var (
	// ErrInvalidOptions reports an invalid dependency or relay option.
	ErrInvalidOptions = errors.New("outbox: invalid options")
	// ErrInvalidMessage reports a malformed or unsafe message.
	ErrInvalidMessage = errors.New("outbox: invalid message")
	// ErrLeaseLost reports that a lease token no longer fences the message.
	ErrLeaseLost = errors.New("outbox: lease lost")
	// ErrStoreContract reports that Store returned values which violate the
	// bounded claiming or fencing contract. The relay fails fast and lets any
	// affected leases expire rather than publishing an indeterminate subset.
	ErrStoreContract = errors.New("outbox: store contract violation")
	// ErrInvalidState reports an invalid relay lifecycle transition.
	ErrInvalidState = errors.New("outbox: invalid state")
	// ErrInvalidRouter reports a malformed immutable route catalog.
	ErrInvalidRouter = errors.New("outbox: invalid router")
)
