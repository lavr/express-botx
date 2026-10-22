package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/httputil"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/metrics"
)

// serveMetrics is the metrics setup of one serve run. With metrics disabled m
// is nil, which every metrics method accepts, and start does nothing.
type serveMetrics struct {
	m      *metrics.Metrics
	listen string
	path   string
}

func newServeMetrics(cfg *config.Config) (*serveMetrics, error) {
	enabled, listen, path, err := cfg.Server.Metrics.Resolve(os.Getenv("EXPRESS_BOTX_SERVER_METRICS_LISTEN"))
	if err != nil {
		return nil, err
	}
	if !enabled {
		return &serveMetrics{}, nil
	}
	return &serveMetrics{m: metrics.New(), listen: listen, path: path}, nil
}

// botxClient returns the HTTP client for BotX calls: observed by the metrics
// when they are enabled, a plain one otherwise.
func (sm *serveMetrics) botxClient(timeout time.Duration) *http.Client {
	if sm.m == nil {
		return httputil.NewClient(timeout)
	}
	return httputil.NewObservedClient(timeout, sm.m.ObserveBotX)
}

// start binds the metrics listener; a failure stops the server from starting.
func (sm *serveMetrics) start(ctx context.Context) error {
	if sm.m == nil {
		return nil
	}
	addr, err := sm.m.Start(ctx, sm.listen, sm.path, vlog.Info)
	if err != nil {
		return fmt.Errorf("metrics: listening on %s: %w", sm.listen, err)
	}
	vlog.Info("serve: metrics at http://%s%s (no authentication; keep it off public networks)", addr, sm.path)
	return nil
}
