package credentials

import "errors"

// ErrInvalid reports a malformed credential reference or secret input.
var ErrInvalid = errors.New("credentials: invalid input")

// ErrNotFound reports that a credential is absent.
var ErrNotFound = errors.New("credentials: not found")

// ErrAlreadyExists reports that Set would overwrite an existing credential.
var ErrAlreadyExists = errors.New("credentials: already exists")

// ErrUnavailable reports that the backing credential service cannot be used.
var ErrUnavailable = errors.New("credentials: store unavailable")

// ErrClosed reports an attempt to read a released secret lease.
var ErrClosed = errors.New("credentials: secret lease closed")
