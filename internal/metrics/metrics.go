// Package metrics holds the server's Prometheus metrics. A *Metrics is created
// only when the metrics endpoint is enabled; every method is safe on a nil
// *Metrics and does nothing, so callers need no "enabled" checks.
//
// No label carries a chat, a message, a key, a query string or anything else
// taken from a request as is: chats are unbounded and would blow up the series
// count, the rest must not leave the process.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "express_botx"

// Delivery outcomes, membership outcomes and sources, as exported in labels.
const (
	Delivered = "delivered"
	Queued    = "queued"
	Failed    = "failed"

	Member    = "member"
	NotMember = "not_member"
	Error     = "error"

	SourceCache  = "cache"
	SourceLookup = "lookup"
)

// botxEndpoints are the BotX API paths this program calls. Any other path is
// reported as "other", so a label value never comes from outside the list.
var botxEndpoints = []string{
	"/api/v2/botx/bots/:id/token",
	"/api/v4/botx/notifications/direct",
	"/api/v3/botx/events/edit_event",
	"/api/v3/botx/chats/list",
	"/api/v3/botx/chats/info",
	"/api/v3/botx/chats/create",
	"/api/v3/botx/chats/add_user",
	"/api/v3/botx/chats/remove_user",
	"/api/v3/botx/chats/add_admin",
	"/api/v3/botx/users/by_huid",
	"/api/v3/botx/users/by_email",
	"/api/v3/botx/users/by_login",
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// Metrics is one registry with the server's collectors.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests      *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	deliveries        *prometheus.CounterVec
	partialDeliveries *prometheus.CounterVec
	edits             *prometheus.CounterVec
	botxRequests      *prometheus.CounterVec
	botxDuration      *prometheus.HistogramVec
	membershipChecks  *prometheus.CounterVec
	membershipLookups *prometheus.CounterVec
	lookupsInflight   prometheus.Gauge
	lookupsRejected   prometheus.Counter
	queuePublishes    *prometheus.CounterVec
	buildInfo         *prometheus.GaugeVec
}

// New returns metrics registered in a fresh registry of their own, with the Go
// runtime and process collectors. Two instances never share counts.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total",
			Help: "HTTP requests served, by method, route pattern and status code.",
		}, []string{"method", "handler", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds",
			Help:    "Time to serve an HTTP request, by method and route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "handler"}),
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "deliveries_total",
			Help: "Per-chat outcomes of send requests: delivered, queued or failed.",
		}, []string{"handler", "outcome"}),
		partialDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "partial_deliveries_total",
			Help: "Send requests answered with success although some of their chats failed.",
		}, []string{"handler"}),
		edits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "edits_total",
			Help: "Message edits, by route pattern and outcome (delivered or failed).",
		}, []string{"handler", "outcome"}),
		botxRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "botx_requests_total",
			Help: "HTTP attempts to the BotX API, retries included, by endpoint and status code " +
				"(\"error\" when no response came back). A 2xx counts the HTTP answer, not a usable result.",
		}, []string{"endpoint", "code"}),
		botxDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "botx_request_duration_seconds",
			Help:    "Time until the BotX API sent response headers, by endpoint.",
			Buckets: prometheus.DefBuckets,
		}, []string{"endpoint"}),
		membershipChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "membership_checks_total",
			Help: "Chat membership checks for chats_where_member keys, by outcome and by source: " +
				"cache, or lookup (a BotX call made or shared, or refused).",
		}, []string{"outcome", "source"}),
		membershipLookups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "membership_lookups_total",
			Help: "Membership lookups actually sent to BotX, by outcome.",
		}, []string{"outcome"}),
		lookupsInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "membership_lookups_inflight",
			Help: "Membership lookups to BotX in progress.",
		}),
		lookupsRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "membership_lookups_rejected_total",
			Help: "Membership checks refused because too many lookups were in progress.",
		}),
		queuePublishes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "queue_publish_total",
			Help: "Publishes to the work queue in enqueue mode, by outcome (ok or error).",
		}, []string{"outcome"}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "build_info",
			Help: "Always 1; the labels tell the version, the Go version and the serve mode (sync or enqueue).",
		}, []string{"version", "goversion", "mode"}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests, m.httpDuration,
		m.deliveries, m.partialDeliveries, m.edits,
		m.botxRequests, m.botxDuration,
		m.membershipChecks, m.membershipLookups, m.lookupsInflight, m.lookupsRejected,
		m.queuePublishes, m.buildInfo,
	)
	// Series that alerts are written against exist from the start at zero,
	// so a rate over them is 0 rather than missing.
	for _, outcome := range []string{Member, NotMember, Error} {
		for _, source := range []string{SourceCache, SourceLookup} {
			m.membershipChecks.WithLabelValues(outcome, source)
		}
		m.membershipLookups.WithLabelValues(outcome)
	}
	m.queuePublishes.WithLabelValues("ok")
	m.queuePublishes.WithLabelValues("error")
	return m
}

// SetBuildInfo publishes the build_info series for this process. mode is
// "sync" or "enqueue".
func (m *Metrics) SetBuildInfo(version, mode string) {
	if m == nil {
		return
	}
	m.buildInfo.Reset()
	m.buildInfo.WithLabelValues(version, runtime.Version(), mode).Set(1)
}

// InitSendRoute creates the zero-valued delivery series of a send route.
func (m *Metrics) InitSendRoute(handler string) {
	if m == nil {
		return
	}
	for _, outcome := range []string{Delivered, Queued, Failed} {
		m.deliveries.WithLabelValues(handler, outcome)
	}
	m.partialDeliveries.WithLabelValues(handler)
}

// ObserveHTTP records one served request. handler must be a route pattern,
// never the raw path. A method outside the standard set is reported as OTHER.
func (m *Metrics) ObserveHTTP(method, handler string, code int, d time.Duration) {
	if m == nil {
		return
	}
	if !knownMethods[method] {
		method = "OTHER"
	}
	m.httpRequests.WithLabelValues(method, handler, strconv.Itoa(code)).Inc()
	m.httpDuration.WithLabelValues(method, handler).Observe(d.Seconds())
}

// Delivery records the outcome for one chat of a send request.
func (m *Metrics) Delivery(handler, outcome string) {
	if m == nil {
		return
	}
	m.deliveries.WithLabelValues(handler, outcome).Inc()
}

// PartialDelivery records a request answered with success while some of its
// chats failed.
func (m *Metrics) PartialDelivery(handler string) {
	if m == nil {
		return
	}
	m.partialDeliveries.WithLabelValues(handler).Inc()
}

// Edit records the outcome of a message edit.
func (m *Metrics) Edit(handler, outcome string) {
	if m == nil {
		return
	}
	m.edits.WithLabelValues(handler, outcome).Inc()
}

// MembershipCheck records a membership check as the server saw it.
func (m *Metrics) MembershipCheck(outcome, source string) {
	if m == nil {
		return
	}
	m.membershipChecks.WithLabelValues(outcome, source).Inc()
}

// LookupStarted and LookupDone bracket one membership lookup sent to BotX.
func (m *Metrics) LookupStarted() {
	if m == nil {
		return
	}
	m.lookupsInflight.Inc()
}

// LookupDone records the outcome of a lookup started with LookupStarted.
func (m *Metrics) LookupDone(outcome string) {
	if m == nil {
		return
	}
	m.lookupsInflight.Dec()
	m.membershipLookups.WithLabelValues(outcome).Inc()
}

// LookupRejected records a check refused for too many lookups in progress.
func (m *Metrics) LookupRejected() {
	if m == nil {
		return
	}
	m.lookupsRejected.Inc()
}

// QueuePublish records one publish to the work queue.
func (m *Metrics) QueuePublish(err error) {
	if m == nil {
		return
	}
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	m.queuePublishes.WithLabelValues(outcome).Inc()
}

// ObserveBotX records one HTTP attempt to the BotX API. It matches
// httputil.Observer and is installed only on clients that talk to BotX.
func (m *Metrics) ObserveBotX(req *http.Request, resp *http.Response, err error, d time.Duration) {
	if m == nil {
		return
	}
	endpoint := BotXEndpoint(req.URL.Path)
	code := "error"
	if err == nil && resp != nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	m.botxRequests.WithLabelValues(endpoint, code).Inc()
	m.botxDuration.WithLabelValues(endpoint).Observe(d.Seconds())
}

// BotXEndpoint maps a request path to one of the known BotX endpoints, or to
// "other". The query string is not part of the path and never consulted.
func BotXEndpoint(path string) string {
	parts := strings.Split(path, "/")
	// The token path carries the bot ID: /api/v2/botx/bots/<id>/token.
	if len(parts) == 7 && parts[4] == "bots" && parts[6] == "token" {
		parts[5] = ":id"
		path = strings.Join(parts, "/")
	}
	for _, e := range botxEndpoints {
		if path == e {
			return e
		}
	}
	return "other"
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Start serves the metrics on their own listener until ctx is done. It binds
// the address before returning, so a taken port fails startup instead of
// surfacing later; serving then runs in the background. The endpoint has no
// authentication: bind it to an address only the scraper reaches. Only path
// is served; there are no debug or other handlers on this listener.
func (m *Metrics) Start(ctx context.Context, listen, path string, logf func(format string, args ...any)) (net.Addr, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("metrics path %q must start with /", path)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	// The path is compared as is, not registered as a ServeMux pattern: a
	// configured value must not turn into a wildcard or a pattern that panics.
	h := m.Handler()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			h.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("metrics: server stopped: %v", err)
		}
	}()
	return ln.Addr(), nil
}

type ctxKey struct{}

// WithContext attaches m to ctx, for code deep in a request (the fan-out) that
// has no other way to reach the server's metrics.
func WithContext(ctx context.Context, m *Metrics) context.Context {
	if m == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, m)
}

// FromContext returns the metrics attached by WithContext, or nil.
func FromContext(ctx context.Context) *Metrics {
	m, _ := ctx.Value(ctxKey{}).(*Metrics)
	return m
}
