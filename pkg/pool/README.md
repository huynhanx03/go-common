# Pool

`pool` provides one calibrated, size-class pool for reusable values, plus
convenience functions for `[]byte`. Use `New` with creation, size, and reset
functions for other types; use `NewResettable` for values that implement
`Resettable`.

The pool groups values into size classes from 64 B to 32 MiB. It periodically
adjusts its preferred and retained sizes from observed `Put` calls. Values
larger than the largest class are allocated directly and discarded on `Put`.

Byte slices can be borrowed with `Get`/`Put`, or scoped with `With`. `With`
returns the slice after the callback finishes, including when it returns an
error or panics. Callbacks must not retain the slice; pooled bytes are not
cleared.
