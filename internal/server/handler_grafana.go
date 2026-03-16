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

// GrafanaConfig holds settings for the Grafana webhook endpoint.
type GrafanaConfig struct {
	DefaultChatID string   // default target chat UUID or alias (may be empty)
	ErrorStates   []string // states that map to status "error"
	Template      *template.Template
	// FallbackChatID is resolved at startup from the config's chats section
	// when there is exactly one chat alias configured. Empty otherwise.
	FallbackChatID string
}

// GrafanaWebhook is the JSON payload from Grafana alerting webhook.
type GrafanaWebhook struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"` // "firing" | "resolved"
	OrgID             int               `json:"orgId"`
	Alerts            []GrafanaAlertItem `json:"alerts"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Title             string            `json:"title"`
	State             string            `json:"state"` // "alerting" | "ok" | "no_data" | "pending"
	Message           string            `json:"message"`
}

// GrafanaAlertItem is a single alert within the Grafana webhook payload.
type GrafanaAlertItem struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	SilenceURL   string            `json:"silenceURL"`
	DashboardURL string            `json:"dashboardURL"`
	PanelURL     string            `json:"panelURL"`
	ImageURL     string            `json:"imageURL"`
	Values       map[string]any    `json:"values"`
}

func (s *Server) handleGrafana(w http.ResponseWriter, r *http.Request) {
	if s.grCfg == nil {
		writeError(w, http.StatusInternalServerError, "grafana not configured")
		return
	}

	var webhook GrafanaWebhook
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if len(webhook.Alerts) == 0 {
		writeError(w, http.StatusBadRequest, "no alerts in payload")
		return
	}

	vlog.V1("grafana: received %s with %d alerts (receiver: %s, state: %s)", webhook.Status, len(webhook.Alerts), webhook.Receiver, webhook.State)
	vlog.V2("grafana: groupKey=%s title=%s", webhook.GroupKey, webhook.Title)

	// Render template
	var buf bytes.Buffer
	if err := s.grCfg.Template.Execute(&buf, webhook); err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}

	message := buf.String()
	vlog.V3("grafana: rendered message:\n%s", message)

	// Determine status
	status := s.resolveGrafanaStatus(webhook)

	// Resolve chat: query param > default_chat_id > global default chat > single chat from config
	targetChat := s.grCfg.DefaultChatID
	if targetChat == "" {
		targetChat = s.cfg.DefaultChatAlias
	}
	if targetChat == "" {
		targetChat = s.grCfg.FallbackChatID
	}
	if q := r.URL.Query().Get("chat_id"); q != "" {
		targetChat = q
	}
	if targetChat == "" {
		writeError(w, http.StatusBadRequest, "chat_id is required: set default_chat_id in config, configure a single chat alias, or pass ?chat_id=")
		return
	}
	chatResult, err := s.chats(targetChat)
	if err != nil {
		writeError(w, http.StatusBadRequest, "resolving chat: "+err.Error())
		return
	}

	// Resolve bot: explicit ?bot= > chat-bound bot > auth bot
	botName, errMsg := s.resolveRequestBot(r.Context(), r.URL.Query().Get("bot"), chatResult.Bot)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	start := time.Now()
	syncID, err := s.send(r.Context(), &SendPayload{
		Bot:     botName,
		ChatID:  chatResult.ChatID,
		Message: message,
		Status:  status,
	})
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if err != nil {
		vlog.V1("grafana: send failed [key: %s] -> 502 (%dms)", keyName, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	vlog.V1("grafana: sent %s to %s [key: %s] -> 200 (%dms)", webhook.Status, targetChat, keyName, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
}

func (s *Server) resolveGrafanaStatus(webhook GrafanaWebhook) string {
	if webhook.Status == "resolved" {
		return "ok"
	}
	errorSet := make(map[string]bool, len(s.grCfg.ErrorStates))
	for _, st := range s.grCfg.ErrorStates {
		errorSet[st] = true
	}
	if errorSet[webhook.State] {
		return "error"
	}
	return "ok"
}

// DefaultGrafanaTemplate is the built-in template for formatting Grafana alerts.
const DefaultGrafanaTemplate = `{{ if eq .Status "firing" }}` + "\U0001F525" + ` FIRING{{ else }}` + "\u2705" + ` RESOLVED{{ end }} {{ .Title }}
{{ range .Alerts }}
{{ if eq .Status "firing" }}` + "\U0001F534" + `{{ else }}` + "\U0001F7E2" + `{{ end }} {{ index .Labels "alertname" }} — {{ index .Annotations "summary" }}
  Folder:   {{ index .Labels "grafana_folder" }}
  Started:  {{ .StartsAt.Format "2006-01-02 15:04:05" }}{{ if ne .Status "firing" }}
  Ended:    {{ .EndsAt.Format "2006-01-02 15:04:05" }}{{ end }}{{ if .DashboardURL }}
  Dashboard: {{ .DashboardURL }}{{ end }}{{ if .PanelURL }}
  Panel:     {{ .PanelURL }}{{ end }}{{ if .SilenceURL }}
  Silence:   {{ .SilenceURL }}{{ end }}
{{ end }}`

// ParseGrafanaTemplate compiles a Go text/template for Grafana messages.
func ParseGrafanaTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("grafana").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing grafana template: %w", err)
	}
	return t, nil
}
