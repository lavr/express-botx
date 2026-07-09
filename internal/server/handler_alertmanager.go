package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// AlertmanagerConfig holds settings for the alertmanager webhook endpoint.
type AlertmanagerConfig struct {
	DefaultChatID   string   // default target chat UUID or alias (may be empty)
	ErrorSeverities []string // severities that map to status "error"
	Template        *template.Template
	// FallbackChatID is resolved at startup from the config's chats section
	// when there is exactly one chat alias configured. Empty otherwise.
	FallbackChatID string
}

// AlertmanagerWebhook is the JSON payload from Alertmanager.
type AlertmanagerWebhook struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"` // "firing" | "resolved"
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []AlertItem       `json:"alerts"`
}

// AlertItem is a single alert within the webhook payload.
type AlertItem struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

func (s *Server) handleAlertmanager(w http.ResponseWriter, r *http.Request) {
	if s.amCfg == nil {
		writeError(w, http.StatusInternalServerError, "alertmanager not configured")
		return
	}

	var webhook AlertmanagerWebhook
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if len(webhook.Alerts) == 0 {
		writeError(w, http.StatusBadRequest, "no alerts in payload")
		return
	}

	vlog.V1("alertmanager: received %s with %d alerts (receiver: %s)", webhook.Status, len(webhook.Alerts), webhook.Receiver)
	vlog.V2("alertmanager: groupKey=%s groupLabels=%v", webhook.GroupKey, webhook.GroupLabels)

	// Render template
	var buf bytes.Buffer
	if err := s.amCfg.Template.Execute(&buf, webhook); err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}

	message := buf.String()
	vlog.V3("alertmanager: rendered message:\n%s", message)

	// Determine status
	status := s.resolveAlertStatus(webhook)

	// Resolve target chats: ?chat_id (now comma-separated, fan-out) > default_chat_id
	// > global default chat > single chat from config. With no ?chat_id the endpoint
	// keeps its single-default behaviour; the response is the uniform
	// MultiSendResponse in every case (results[0] for a single chat).
	targets := parseChatIDs(r.URL.Query().Get("chat_id"))
	// An explicitly-present but empty chat_id (?chat_id= / ?chat_id=,,) is a
	// request error, not a silent fall-back to the default chat.
	if len(targets) == 0 && r.URL.Query().Has("chat_id") {
		writeError(w, http.StatusBadRequest, "chat_id is empty: provide at least one chat, or omit chat_id to use the default")
		return
	}
	if len(targets) == 0 {
		if single := s.amCfg.singleChat(s.cfg.DefaultChatAlias); single != "" {
			targets = []string{single}
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "chat_id is required: set default_chat_id in config, configure a single chat alias, or pass ?chat_id=")
		return
	}

	start := time.Now()
	results, errs := s.fanoutSend(r.Context(), targets, r.URL.Query().Get("bot"), message, status)
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if len(results) == 0 {
		vlog.V1("alertmanager: fan-out to %d chats all failed [key: %s] -> 502 (%dms)", len(targets), keyName, elapsed.Milliseconds())
	} else {
		vlog.V1("alertmanager: sent %s to %d/%d chats [key: %s] (%dms)", webhook.Status, len(results), len(targets), keyName, elapsed.Milliseconds())
	}
	writeMultiSend(w, results, errs, http.StatusOK)
}

// singleChat returns the fallback delivery chat for alertmanager, following the
// precedence default_chat_id -> global default chat -> the sole configured chat
// alias. It is empty when none is configured.
func (c *AlertmanagerConfig) singleChat(globalDefault string) string {
	if c.DefaultChatID != "" {
		return c.DefaultChatID
	}
	if globalDefault != "" {
		return globalDefault
	}
	return c.FallbackChatID
}

func (s *Server) resolveAlertStatus(webhook AlertmanagerWebhook) string {
	if webhook.Status == "resolved" {
		return "ok"
	}
	errorSet := make(map[string]bool, len(s.amCfg.ErrorSeverities))
	for _, sev := range s.amCfg.ErrorSeverities {
		errorSet[sev] = true
	}
	for _, a := range webhook.Alerts {
		if errorSet[a.Labels["severity"]] {
			return "error"
		}
	}
	return "ok"
}

// DefaultAlertmanagerTemplate is the built-in template for formatting alerts.
const DefaultAlertmanagerTemplate = `{{ if eq .Status "firing" }}` + "\U0001F525" + ` FIRING{{ else }}` + "\u2705" + ` RESOLVED{{ end }} [{{ index .GroupLabels "alertname" }}]
{{ range .Alerts }}
{{ if eq .Status "firing" }}` + "\U0001F534" + `{{ else }}` + "\U0001F7E2" + `{{ end }} {{ index .Labels "alertname" }} — {{ index .Annotations "summary" }}
  Severity: {{ index .Labels "severity" }}
  Instance: {{ index .Labels "instance" }}
  Started:  {{ .StartsAt.Format "2006-01-02 15:04:05" }}{{ if ne .Status "firing" }}
  Ended:    {{ .EndsAt.Format "2006-01-02 15:04:05" }}{{ end }}
{{ end }}`

// ParseAlertmanagerTemplate compiles a Go text/template for alertmanager messages.
func ParseAlertmanagerTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("alertmanager").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing alertmanager template: %w", err)
	}
	return t, nil
}
