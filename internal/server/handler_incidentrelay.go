package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
)

const (
	IncidentRelayMessageSourceTemplate = config.IncidentRelayMessageSourceTemplate
	IncidentRelayMessageSourceWebhook  = config.IncidentRelayMessageSourceWebhook
)

type IncidentRelayConfig struct {
	DefaultChatID   string
	ErrorSeverities []string
	Template        *template.Template
	MessageSource   string
	FallbackChatID  string
}

type IncidentRelayLink struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

type IncidentRelayRunbook struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Severity string `json:"severity"`
}

type IncidentRelayWebhook struct {
	Text            string                 `json:"text"`
	AlertID         int                    `json:"alert_id"`
	AlertURL        string                 `json:"alert_url"`
	SourceEventURL  string                 `json:"source_event_url"`
	Team            string                 `json:"team"`
	Status          string                 `json:"status"`
	Source          string                 `json:"source"`
	Title           string                 `json:"title"`
	Message         string                 `json:"message"`
	Severity        string                 `json:"severity"`
	Priority        string                 `json:"priority"`
	PriorityLabel   string                 `json:"priority_label"`
	Assignee        string                 `json:"assignee"`
	Service         string                 `json:"service"`
	ServiceID       int                    `json:"service_id"`
	ServiceName     string                 `json:"service_name"`
	ServiceSlug     string                 `json:"service_slug"`
	ServiceLinks    []IncidentRelayLink    `json:"service_links"`
	ServiceRunbooks []IncidentRelayRunbook `json:"service_runbooks"`
	Correlation     json.RawMessage        `json:"correlation"`
}

func (s *Server) handleIncidentRelay(w http.ResponseWriter, r *http.Request) {
	if s.irCfg == nil {
		writeError(w, http.StatusInternalServerError, "incidentrelay not configured")
		return
	}

	trace := s.newPayloadTrace(r)
	body, err := readRawBody(r, trace, "incidentrelay")
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body: "+err.Error())
		return
	}

	var webhook IncidentRelayWebhook
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&webhook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if webhook.Text == "" && webhook.Title == "" && webhook.Message == "" {
		writeError(w, http.StatusBadRequest, "no content in payload: text, title and message are all empty")
		return
	}

	vlog.V1("incidentrelay: received %s alert %d (source: %s, severity: %s)", webhook.Status, webhook.AlertID, webhook.Source, webhook.Severity)
	vlog.V2("incidentrelay: team=%s service=%s assignee=%s", webhook.Team, webhook.Service, webhook.Assignee)

	message, err := s.renderIncidentRelayMessage(webhook)
	if err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}
	if strings.TrimSpace(message) == "" {
		writeError(w, http.StatusBadRequest, "rendered message is empty")
		return
	}
	trace.renderedMessage("incidentrelay", message)

	status := s.resolveIncidentRelayStatus(webhook)

	targets := parseChatIDs(r.URL.Query().Get("chat_id"))
	if len(targets) == 0 && r.URL.Query().Has("chat_id") {
		writeError(w, http.StatusBadRequest, "chat_id is empty: provide at least one chat, or omit chat_id to use the default")
		return
	}
	if len(targets) == 0 {
		if single := s.irCfg.singleChat(s.cfg.DefaultChatAlias); single != "" {
			targets = []string{single}
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "chat_id is required: set default_chat_id in config, configure a single chat alias, or pass ?chat_id=")
		return
	}

	if !s.authorizeTargets(w, r, targets, true) {
		return
	}

	start := time.Now()
	results, errs := s.fanoutSend(r.Context(), targets, r.URL.Query().Get("bot"), message, status)
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if len(results) == 0 {
		vlog.V1("incidentrelay: fan-out to %d chats all failed [key: %s] -> 502 (%dms)", len(targets), keyName, elapsed.Milliseconds())
	} else {
		vlog.V1("incidentrelay: sent %s to %d/%d chats [key: %s] (%dms)", webhook.Status, len(results), len(targets), keyName, elapsed.Milliseconds())
	}
	writeMultiSend(w, results, s.sanitizeErrors(r.Context(), errs), http.StatusOK)
}

func (c *IncidentRelayConfig) singleChat(globalDefault string) string {
	if c.DefaultChatID != "" {
		return c.DefaultChatID
	}
	if globalDefault != "" {
		return globalDefault
	}
	return c.FallbackChatID
}

func (s *Server) renderIncidentRelayMessage(webhook IncidentRelayWebhook) (string, error) {
	if s.irCfg.MessageSource != IncidentRelayMessageSourceTemplate {
		if webhook.Text != "" {
			return webhook.Text, nil
		}
		if webhook.Title != "" && webhook.Message != "" {
			return webhook.Title + "\n\n" + webhook.Message, nil
		}
		if webhook.Title != "" {
			return webhook.Title, nil
		}
		if webhook.Message != "" {
			return webhook.Message, nil
		}
	}

	var buf bytes.Buffer
	if err := s.irCfg.Template.Execute(&buf, webhook); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) resolveIncidentRelayStatus(webhook IncidentRelayWebhook) string {
	if webhook.Status == "resolved" {
		return "ok"
	}
	errorSet := make(map[string]bool, len(s.irCfg.ErrorSeverities))
	for _, sev := range s.irCfg.ErrorSeverities {
		errorSet[sev] = true
	}
	if errorSet[webhook.Severity] {
		return "error"
	}
	return "ok"
}

const DefaultIncidentRelayTemplate = `{{ if eq .Status "resolved" }}` + "✅" + ` RESOLVED{{ else }}` + "\U0001F525" + ` {{ .Status | printf "%s" }}{{ end }} {{ .Title }}
  Severity: {{ .Severity }}{{ if .PriorityLabel }}
  Priority: {{ .PriorityLabel }}{{ end }}{{ if .Team }}
  Team:     {{ .Team }}{{ end }}{{ if .Service }}
  Service:  {{ .Service }}{{ end }}{{ if .Assignee }}
  Assignee: {{ .Assignee }}{{ end }}{{ if .Source }}
  Source:   {{ .Source }}{{ end }}{{ if .Message }}

{{ .Message }}{{ end }}{{ if .AlertURL }}
  Alert:    {{ .AlertURL }}{{ end }}{{ range .ServiceRunbooks }}
  Runbook:  {{ .Title }} {{ .URL }}{{ end }}`

func ParseIncidentRelayTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("incidentrelay").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing incidentrelay template: %w", err)
	}
	return t, nil
}
