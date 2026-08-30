package ipp

import (
	"errors"
	"fmt"

	"github.com/OpenPrinting/goipp"
)

// Errors from this package come in two kinds, and the difference matters.
//
// A transport failure means the exchange itself did not happen: the socket was
// refused, the server answered with a non-200, the response would not decode.
// Those come back as plain wrapped errors.
//
// An [Error] means the exchange worked perfectly and CUPS said no. Those carry
// the IPP status code and unwrap to one of the sentinels below, so callers can
// branch with errors.Is without importing goipp or knowing hex status codes.
//
// # Mapping onto the connector protocol
//
// PROTOCOL.md section 3 defines the JSON-RPC error codes connectors see. That
// translation deliberately does NOT live here, because it cannot be done from a
// status code alone: IPP answers a missing printer and a missing job with the
// same not-found, while the protocol distinguishes -32003 (unknown printer) from
// -32004 (unknown job). Only the caller knows which it asked for. The mapping
// therefore belongs at the protocol boundary, in Stage 31, where that context
// exists. Intended correspondence:
//
//	ErrNotFound          -> -32003 or -32004, by what was requested
//	ErrForbidden         -> -32001 scope denied
//	ErrNotAuthenticated  -> -32002 not authenticated
//	ErrFormatUnsupported -> -32007 payload rejected
var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("forbidden")
	ErrNotAuthenticated  = errors.New("not authenticated")
	ErrNotAuthorized     = errors.New("not authorized")
	ErrNotPossible       = errors.New("not possible")
	ErrBadRequest        = errors.New("bad request")
	ErrConflict          = errors.New("conflicting attributes")
	ErrFormatUnsupported = errors.New("document format not supported")
	ErrTimeout           = errors.New("timeout")
	ErrGone              = errors.New("gone")

	// ErrServer covers the whole server-error range. These are the server's
	// problem rather than the request's, so they are the ones worth retrying.
	ErrServer = errors.New("server error")
)

// Error is an IPP-level failure reported by the server.
type Error struct {
	Op     goipp.Op
	Status goipp.Status

	// Message is the server's status-message, when it sent one. CUPS often does,
	// and it is usually more specific than the status code.
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("ipp: %s: %s: %s", e.Op, e.Status, e.Message)
	}
	return fmt.Sprintf("ipp: %s: %s", e.Op, e.Status)
}

// Unwrap exposes the sentinel so errors.Is works.
func (e *Error) Unwrap() error { return sentinel(e.Status) }

func sentinel(s goipp.Status) error {
	switch s {
	case goipp.StatusErrorNotFound:
		return ErrNotFound
	case goipp.StatusErrorForbidden:
		return ErrForbidden
	case goipp.StatusErrorNotAuthenticated:
		return ErrNotAuthenticated
	case goipp.StatusErrorNotAuthorized:
		return ErrNotAuthorized
	case goipp.StatusErrorNotPossible:
		return ErrNotPossible
	case goipp.StatusErrorBadRequest:
		return ErrBadRequest
	case goipp.StatusErrorConflicting:
		return ErrConflict
	case goipp.StatusErrorDocumentFormatNotSupported:
		return ErrFormatUnsupported
	case goipp.StatusErrorTimeout:
		return ErrTimeout
	case goipp.StatusErrorGone:
		return ErrGone
	}

	// 0x0500 and above is the server-error range, RFC 8011 section 5.1.5.
	if s >= 0x0500 {
		return ErrServer
	}
	return nil
}

// StatusOf returns the IPP status carried by err, if it has one.
func StatusOf(err error) (goipp.Status, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Status, true
	}
	return 0, false
}

// check turns a response status into an error, or nil when the operation
// succeeded.
//
// RFC 8011 section 5.1.5 makes this a range test rather than a list: every code
// below 0x0100 is a success. Several of them are successes with a caveat, such
// as attributes being ignored or substituted, and treating those as failures
// would reject perfectly good print jobs for cosmetic reasons.
func check(op goipp.Op, resp *goipp.Message) error {
	status := goipp.Status(resp.Code)
	if status < 0x0100 {
		return nil
	}
	return &Error{
		Op:      op,
		Status:  status,
		Message: str(resp.Operation, "status-message"),
	}
}
