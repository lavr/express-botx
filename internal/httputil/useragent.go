package httputil

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// Observer is told about each HTTP attempt made through an observed client,
// once response headers arrive or the attempt fails (resp is nil when err is
// not). It does not see the body being read.
type Observer func(req *http.Request, resp *http.Response, err error, d time.Duration)

// version is set by SetVersion during startup.
var version = "dev"

// SetVersion sets the application version used in the User-Agent header.
func SetVersion(v string) {
	version = v
}

// UserAgent returns the User-Agent string:
//
//	express-botx/1.2.3 Go/1.23.4 linux/amd64
func UserAgent() string {
	return fmt.Sprintf("express-botx/%s %s %s/%s",
		version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// Transport returns an http.RoundTripper that sets the User-Agent header
// on every outgoing request. It wraps the provided base transport;
// if base is nil, http.DefaultTransport is used.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &uaTransport{base: base}
}

type uaTransport struct {
	base     http.RoundTripper
	observer Observer
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent())
	}
	if t.observer == nil {
		return t.base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	t.observer(req, resp, err, time.Since(start))
	return resp, err
}

// NewClient returns an *http.Client with the User-Agent transport and the given timeout.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: Transport(nil),
	}
}

// NewObservedClient is NewClient with obs told about every attempt; a nil obs
// gives a plain NewClient.
func NewObservedClient(timeout time.Duration, obs Observer) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &uaTransport{base: http.DefaultTransport, observer: obs},
	}
}
