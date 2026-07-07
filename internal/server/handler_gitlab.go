package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// GitlabConfig holds settings for the GitLab webhook endpoint.
type GitlabConfig struct {
	DefaultChatID string // default target chat UUID or alias (may be empty)
	// SecretToken is the expected value of the X-Gitlab-Token header. GitLab
	// cannot send Authorization/X-API-Key headers, so the /gitlab route uses
	// this token instead of the standard API-key middleware.
	SecretToken string
	Template    *template.Template
	// FallbackChatID is resolved at startup from the config's chats section
	// when there is exactly one chat alias configured. Empty otherwise.
	FallbackChatID string
}

// GitlabWebhook is the subset of GitLab group/project webhook payloads we use.
// It covers both merge_request and note events (fields not present in a given
// event kind are simply left zero).
type GitlabWebhook struct {
	ObjectKind       string              `json:"object_kind"`
	User             GitlabUser          `json:"user"`
	Project          GitlabProject       `json:"project"`
	ObjectAttributes GitlabObjectAttrs   `json:"object_attributes"`
	MergeRequest     *GitlabMergeRequest `json:"merge_request"` // present on note events about MRs
}

// GitlabUser is the actor that triggered the event.
type GitlabUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

// GitlabProject is the project the event belongs to.
type GitlabProject struct {
	Name   string `json:"name"`
	WebURL string `json:"web_url"`
}

// GitlabObjectAttrs holds the object_attributes block, shared between event kinds.
type GitlabObjectAttrs struct {
	Action              string `json:"action"`        // merge_request: open|merge|update|close|...
	Title               string `json:"title"`         // merge_request
	URL                 string `json:"url"`           // merge_request / note
	SourceBranch        string `json:"source_branch"` // merge_request
	TargetBranch        string `json:"target_branch"` // merge_request
	DetailedMergeStatus string `json:"detailed_merge_status"`
	NoteableType        string `json:"noteable_type"` // note: "MergeRequest", "Commit", ...
	Note                string `json:"note"`          // note: comment text
	System              bool   `json:"system"`        // note: true for system-generated notes
}

// GitlabMergeRequest is the merge_request block nested inside note events.
type GitlabMergeRequest struct {
	Title        string `json:"title"`
	URL          string `json:"url"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

// gitlabView is the view-model passed to the message template.
type gitlabView struct {
	Event        string // "open" | "merge" | "comment"
	Author       string
	Project      string
	Title        string
	URL          string
	SourceBranch string
	TargetBranch string
	MergeStatus  string
	Comment      string // comment text (Event == "comment")
}

// classifyGitlab maps a webhook into a view-model, or returns ok=false when the
// event should be ignored (200 OK without sending anything).
func classifyGitlab(w GitlabWebhook) (gitlabView, bool) {
	author := w.User.Name
	if author == "" {
		author = w.User.Username
	}

	switch w.ObjectKind {
	case "merge_request":
		switch w.ObjectAttributes.Action {
		case "open", "merge":
			return gitlabView{
				Event:        w.ObjectAttributes.Action,
				Author:       author,
				Project:      w.Project.Name,
				Title:        w.ObjectAttributes.Title,
				URL:          w.ObjectAttributes.URL,
				SourceBranch: w.ObjectAttributes.SourceBranch,
				TargetBranch: w.ObjectAttributes.TargetBranch,
				MergeStatus:  w.ObjectAttributes.DetailedMergeStatus,
			}, true
		}
	case "note":
		// Only comments on merge requests, excluding system-generated notes.
		if w.ObjectAttributes.NoteableType == "MergeRequest" && !w.ObjectAttributes.System {
			v := gitlabView{
				Event:   "comment",
				Author:  author,
				Project: w.Project.Name,
				URL:     w.ObjectAttributes.URL,
				Comment: w.ObjectAttributes.Note,
			}
			if w.MergeRequest != nil {
				v.Title = w.MergeRequest.Title
				v.SourceBranch = w.MergeRequest.SourceBranch
				v.TargetBranch = w.MergeRequest.TargetBranch
				if v.URL == "" {
					v.URL = w.MergeRequest.URL
				}
			}
			return v, true
		}
	}
	return gitlabView{}, false
}

func (s *Server) handleGitlab(w http.ResponseWriter, r *http.Request) {
	if s.gitCfg == nil {
		writeError(w, http.StatusInternalServerError, "gitlab not configured")
		return
	}

	// Verify X-Gitlab-Token (GitLab cannot set Authorization/X-API-Key).
	// Reject empty tokens outright so a misconfigured empty SecretToken never
	// authenticates an unauthenticated request via the constant-time compare.
	token := r.Header.Get("X-Gitlab-Token")
	if s.gitCfg.SecretToken == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(s.gitCfg.SecretToken)) != 1 {
		vlog.V1("gitlab: token mismatch -> 401")
		writeError(w, http.StatusUnauthorized, "invalid gitlab token")
		return
	}

	var webhook GitlabWebhook
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	vlog.V1("gitlab: received %s (action: %s, noteable: %s)", webhook.ObjectKind, webhook.ObjectAttributes.Action, webhook.ObjectAttributes.NoteableType)

	view, ok := classifyGitlab(webhook)
	if !ok {
		vlog.V2("gitlab: ignored %s event (action: %s)", webhook.ObjectKind, webhook.ObjectAttributes.Action)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "ignored": true})
		return
	}

	// Render template
	var buf bytes.Buffer
	if err := s.gitCfg.Template.Execute(&buf, view); err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}
	message := buf.String()
	vlog.V3("gitlab: rendered message:\n%s", message)

	// Resolve chat: query param > default_chat_id > global default chat > single chat from config
	targetChat := s.gitCfg.DefaultChatID
	if targetChat == "" {
		targetChat = s.cfg.DefaultChatAlias
	}
	if targetChat == "" {
		targetChat = s.gitCfg.FallbackChatID
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
		Status:  "ok",
	})
	elapsed := time.Since(start)

	if err != nil {
		vlog.V1("gitlab: send failed -> 502 (%dms)", elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	vlog.V1("gitlab: sent %s to %s -> 200 (%dms)", view.Event, targetChat, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
}

// DefaultGitlabTemplate is the built-in template for formatting GitLab MR events.
const DefaultGitlabTemplate = `{{ if eq .Event "open" }}` + "\U0001F195" + ` Новый MR{{ else if eq .Event "merge" }}` + "✅" + ` MR слит{{ else }}` + "\U0001F4AC" + ` Комментарий в MR{{ end }}{{ if .Project }} [{{ .Project }}]{{ end }}
{{ if eq .Event "comment" }}{{ .Author }}: {{ .Comment }}
{{ if .Title }}MR: {{ .Title }}
{{ end }}{{ else }}{{ .Title }}
  Автор:  {{ .Author }}
  Ветки:  {{ .SourceBranch }} -> {{ .TargetBranch }}{{ if eq .Event "merge" }}
  Статус: Успешно слито{{ else if .MergeStatus }}
  Статус: {{ .MergeStatus }}{{ end }}
{{ end }}{{ if .URL }}{{ .URL }}{{ end }}`

// ParseGitlabTemplate compiles a Go text/template for GitLab messages.
func ParseGitlabTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("gitlab").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing gitlab template: %w", err)
	}
	return t, nil
}
