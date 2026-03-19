package apm

import "net/http"

// Transaction represents an APM transaction for non-HTTP workloads (e.g. queue message processing).
type Transaction interface {
	End()
}

// Provider is an APM provider. The implementation is selected at compile time via build tags.
type Provider interface {
	// WrapHandler wraps an HTTP handler for tracing.
	WrapHandler(pattern string, h http.Handler) http.Handler
	// StartTransaction begins a named transaction for non-HTTP work (e.g. worker message processing).
	StartTransaction(name string) Transaction
	// Shutdown flushes pending data and stops the agent.
	Shutdown()
}
