//go:build !newrelic && !elasticapm

package apm

import "net/http"

type noopProvider struct{}

// New returns a no-op APM provider.
func New() Provider { return noopProvider{} }

func (noopProvider) WrapHandler(_ string, h http.Handler) http.Handler { return h }
func (noopProvider) StartTransaction(string) Transaction               { return noopTxn{} }
func (noopProvider) Shutdown()                                          {}

type noopTxn struct{}

func (noopTxn) End() {}
