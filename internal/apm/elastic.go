//go:build elasticapm

package apm

import (
	"net/http"

	"go.elastic.co/apm/v2"
	"go.elastic.co/apm/module/apmhttp/v2"
)

type elasticProvider struct {
	tracer *apm.Tracer
}

// New creates an Elastic APM provider.
// Configuration is read from standard Elastic APM environment variables:
//   - ELASTIC_APM_SERVICE_NAME (required)
//   - ELASTIC_APM_SERVER_URL (required)
//   - ELASTIC_APM_SECRET_TOKEN or ELASTIC_APM_API_KEY
//   - ELASTIC_APM_ENVIRONMENT
func New() Provider {
	tracer, err := apm.NewTracerOptions(apm.TracerOptions{})
	if err != nil {
		return noopFallback{}
	}
	return &elasticProvider{tracer: tracer}
}

func (p *elasticProvider) WrapHandler(pattern string, h http.Handler) http.Handler {
	return apmhttp.Wrap(h,
		apmhttp.WithTracer(p.tracer),
		apmhttp.WithServerRequestName(func(_ *http.Request) string { return pattern }),
	)
}

func (p *elasticProvider) StartTransaction(name string) Transaction {
	tx := p.tracer.StartTransaction(name, "worker")
	return &elasticTxn{tx: tx}
}

type elasticTxn struct {
	tx *apm.Transaction
}

func (t *elasticTxn) End() { t.tx.End() }

func (p *elasticProvider) Shutdown() {
	p.tracer.Flush(nil)
	p.tracer.Close()
}

type noopFallback struct{}

func (noopFallback) WrapHandler(_ string, h http.Handler) http.Handler { return h }
func (noopFallback) StartTransaction(string) Transaction               { return noopTxn{} }
func (noopFallback) Shutdown()                                          {}

type noopTxn struct{}

func (noopTxn) End() {}
