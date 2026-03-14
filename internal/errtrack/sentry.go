//go:build sentry

package errtrack

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

type sentryTracker struct {
	handler *sentryhttp.Handler
}

// New creates a Sentry error tracker.
// Configuration is read from standard Sentry environment variables:
//   - SENTRY_DSN (required)
//   - SENTRY_ENVIRONMENT
//   - SENTRY_RELEASE
//
// If SENTRY_DSN is empty or initialization fails, returns a no-op tracker.
func New() Tracker {
	err := sentry.Init(sentry.ClientOptions{})
	if err != nil {
		return noopFallback{}
	}
	return &sentryTracker{
		handler: sentryhttp.New(sentryhttp.Options{
			Repanic: true,
		}),
	}
}

func (t *sentryTracker) Middleware(h http.Handler) http.Handler {
	return t.handler.Handle(h)
}

func (t *sentryTracker) CaptureError(err error) {
	sentry.CaptureException(err)
}

func (t *sentryTracker) Flush() {
	sentry.Flush(5 * time.Second)
}

type noopFallback struct{}

func (noopFallback) Middleware(h http.Handler) http.Handler { return h }
func (noopFallback) CaptureError(_ error)                   {}
func (noopFallback) Flush()                                  {}
