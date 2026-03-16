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

	// Resolve chat: query param > default_chat_id > global default chat > single chat from config
	targetChat := s.amCfg.DefaultChatID
	if targetChat == "" {
		targetChat = s.cfg.DefaultChatAlias
	}
	if targetChat == "" {
		targetChat = s.amCfg.FallbackChatID
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
		vlog.V1("alertmanager: send failed [key: %s] -> 502 (%dms)", keyName, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	vlog.V1("alertmanager: sent %s to %s [key: %s] -> 200 (%dms)", webhook.Status, targetChat, keyName, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
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
