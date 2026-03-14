//go:build !sentry

package errtrack

import "net/http"

type noopTracker struct{}

// New returns a no-op error tracker.
func New() Tracker { return noopTracker{} }

func (noopTracker) Middleware(h http.Handler) http.Handler { return h }
func (noopTracker) CaptureError(_ error)                   {}
func (noopTracker) Flush()                                  {}
