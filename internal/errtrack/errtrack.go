package errtrack

import "net/http"

// Tracker captures panics and errors in HTTP handlers.
// The implementation is selected at compile time via build tags.
type Tracker interface {
	// Middleware wraps an http.Handler to capture panics and report errors.
	Middleware(h http.Handler) http.Handler
	// CaptureError reports an error to the error tracking service.
	CaptureError(err error)
	// Flush waits for pending events to be sent and shuts down.
	Flush()
}
