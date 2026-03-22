package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// slogLogFormatter implements chi's middleware.LogFormatter using log/slog
// for structured JSON access logging.
type slogLogFormatter struct {
	logger *slog.Logger
}

func (f *slogLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &slogLogEntry{
		logger: f.logger,
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		remote: r.RemoteAddr,
		proto:  r.Proto,
		reqID:  middleware.GetReqID(r.Context()),
	}
}

type slogLogEntry struct {
	logger                            *slog.Logger
	method, path, query, remote, proto, reqID string
}

func (e *slogLogEntry) Write(status, bytes int, header http.Header, elapsed time.Duration, extra interface{}) {
	attrs := []slog.Attr{
		slog.String("method", e.method),
		slog.String("path", e.path),
		slog.Int("status", status),
		slog.Int("bytes", bytes),
		slog.Float64("duration", elapsed.Seconds()),
		slog.String("remote", e.remote),
	}
	if e.reqID != "" {
		attrs = append(attrs, slog.String("request_id", e.reqID))
	}
	if e.query != "" {
		attrs = append(attrs, slog.String("query", e.query))
	}

	level := slog.LevelInfo
	if status >= 500 {
		level = slog.LevelError
	} else if status >= 400 {
		level = slog.LevelWarn
	}

	e.logger.LogAttrs(nil, level, e.proto, attrs...)
}

func (e *slogLogEntry) Panic(v interface{}, stack []byte) {
	e.logger.LogAttrs(nil, slog.LevelError, "panic",
		slog.Any("error", v),
		slog.String("stack", string(stack)),
		slog.String("request_id", e.reqID),
	)
}
