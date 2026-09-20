package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	vlog "github.com/lavr/express-botx/internal/log"
)

const (
	maxRawBodyLogBytes = 64 << 10
	tracePayloadParam  = "trace"
	tracePayloadHeader = "X-Botx-Trace"
)

type payloadTrace struct {
	forced bool
	id     string
}

func (s *Server) newPayloadTrace(r *http.Request) payloadTrace {
	return payloadTrace{
		forced: s.cfg.AllowRequestTrace && tracePayloadRequested(r),
		id:     middleware.GetReqID(r.Context()),
	}
}

func tracePayloadRequested(r *http.Request) bool {
	if q := r.URL.Query(); q.Has(tracePayloadParam) {
		return traceFlagValue(q.Get(tracePayloadParam), true)
	}
	return traceFlagValue(r.Header.Get(tracePayloadHeader), false)
}

func traceFlagValue(v string, bareMeansOn bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return bareMeansOn
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (t payloadTrace) enabled() bool {
	return t.forced || vlog.Level >= 3
}

func (t payloadTrace) rawPayload(source string, body []byte) {
	if !t.enabled() {
		return
	}
	t.log(source, "raw payload", body)
}

func (t payloadTrace) renderedMessage(source, message string) {
	if !t.enabled() {
		return
	}
	t.log(source, "rendered message", []byte(message))
}

func (t payloadTrace) log(source, kind string, data []byte) {
	notes := make([]string, 0, 3)
	if t.id != "" {
		notes = append(notes, "id="+t.id)
	}
	if t.forced {
		notes = append(notes, "request_trace=true")
	}
	if len(data) > maxRawBodyLogBytes {
		notes = append(notes, fmt.Sprintf("truncated to %d of %d bytes", maxRawBodyLogBytes, len(data)))
		data = data[:maxRawBodyLogBytes]
	}

	header := source + ": " + kind
	if len(notes) > 0 {
		header += " [" + strings.Join(notes, " ") + "]"
	}
	if t.forced {
		vlog.Info("%s:\n%s", header, data)
		return
	}
	vlog.V3("%s:\n%s", header, data)
}

func readRawBody(r *http.Request, trace payloadTrace, source string) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	trace.rawPayload(source, body)
	return body, nil
}
