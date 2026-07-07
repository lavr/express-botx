package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	// Only and Exclude filter incoming events by event key. Each entry matches
	// a full event key ("kind.subtype"), a bare kind (all subtypes), or a
	// "kind.*" wildcard. An empty Only allows every event; Exclude always wins.
	Only    []string
	Exclude []string
}

// gitlabView is the view-model passed to the message template. It is derived
// best-effort from an arbitrary GitLab webhook payload; Raw carries the full
// decoded payload so templates can reach fields not surfaced here (via the
// `get` helper or the .Get method).
type gitlabView struct {
	Kind     string         // object_kind, e.g. "merge_request", "note", "push"
	Action   string         // object_attributes.action, or the derived subtype
	EventKey string         // "kind" or "kind.subtype"
	Project  string         // project.name (fallback project.path_with_namespace)
	User     string         // user.name / user.username (fallback user_name)
	Title    string         // object_attributes.title
	URL      string         // object_attributes.url (fallback project.web_url)
	Raw      map[string]any // full decoded payload
}

// Get returns the value at a dotted path inside the raw payload, or nil when
// any path segment is missing. It mirrors the `get` template helper so
// templates can use either `{{ .Get "a.b.c" }}` or `{{ get .Raw "a.b.c" }}`.
func (v gitlabView) Get(path string) any {
	return gitlabNestedGet(v.Raw, path)
}

// gitlabNestedGet walks a dotted path (e.g. "object_attributes.url") through a
// decoded JSON object and returns the value found, or nil when any segment is
// absent or not an object.
func gitlabNestedGet(m map[string]any, path string) any {
	if m == nil || path == "" {
		return nil
	}
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return cur
}

// gitlabStringAt returns the string value at a dotted path, or "" when it is
// missing or not a string.
func gitlabStringAt(m map[string]any, path string) string {
	if s, ok := gitlabNestedGet(m, path).(string); ok {
		return s
	}
	return ""
}

// deriveEventKey computes the GitLab event key from a raw payload. The subtype
// rule depends on object_kind:
//
//	merge_request, issue -> object_attributes.action
//	note                 -> object_attributes.noteable_type
//	pipeline             -> object_attributes.status
//	build (job)          -> build_status (a flat top-level field)
//	push, tag_push       -> none
//	otherwise            -> object_attributes.action, then object_attributes.status
//
// eventKey is the bare kind when there is no subtype, otherwise "kind.subtype".
// A payload without object_kind yields an empty eventKey.
func deriveEventKey(raw map[string]any) (kind, subtype, eventKey string) {
	kind = gitlabStringAt(raw, "object_kind")
	switch kind {
	case "merge_request", "issue":
		subtype = gitlabStringAt(raw, "object_attributes.action")
	case "note":
		subtype = gitlabStringAt(raw, "object_attributes.noteable_type")
	case "pipeline":
		subtype = gitlabStringAt(raw, "object_attributes.status")
	case "build":
		subtype = gitlabStringAt(raw, "build_status")
	case "push", "tag_push":
		// No subtype for push-style events.
	default:
		if s := gitlabStringAt(raw, "object_attributes.action"); s != "" {
			subtype = s
		} else {
			subtype = gitlabStringAt(raw, "object_attributes.status")
		}
	}

	switch {
	case kind == "":
		eventKey = ""
	case subtype == "":
		eventKey = kind
	default:
		eventKey = kind + "." + subtype
	}
	return kind, subtype, eventKey
}

// normalizeGitlab derives a gitlabView from an arbitrary payload, filling common
// fields best-effort and keeping the full payload in Raw.
func normalizeGitlab(raw map[string]any) gitlabView {
	kind, subtype, eventKey := deriveEventKey(raw)
	v := gitlabView{
		Kind:     kind,
		EventKey: eventKey,
		Raw:      raw,
	}

	// Action: prefer the literal object_attributes.action, else the derived subtype.
	if a := gitlabStringAt(raw, "object_attributes.action"); a != "" {
		v.Action = a
	} else {
		v.Action = subtype
	}

	// Project name.
	v.Project = gitlabStringAt(raw, "project.name")
	if v.Project == "" {
		v.Project = gitlabStringAt(raw, "project.path_with_namespace")
	}

	// Acting user.
	v.User = gitlabStringAt(raw, "user.name")
	if v.User == "" {
		v.User = gitlabStringAt(raw, "user.username")
	}
	if v.User == "" {
		// push / tag_push events carry a flat user_name field.
		v.User = gitlabStringAt(raw, "user_name")
	}

	// Title and URL.
	v.Title = gitlabStringAt(raw, "object_attributes.title")
	v.URL = gitlabStringAt(raw, "object_attributes.url")
	if v.URL == "" {
		v.URL = gitlabStringAt(raw, "project.web_url")
	}

	return v
}

// eventMatches reports whether the event identified by kind/eventKey matches any
// entry in entries. An entry matches when it equals the full event key
// ("kind.subtype"), the bare kind (i.e. every subtype of that kind), or the
// "kind.*" wildcard form.
func eventMatches(kind, eventKey string, entries []string) bool {
	for _, e := range entries {
		if e == eventKey || e == kind || e == kind+".*" {
			return true
		}
	}
	return false
}

// passesFilter applies the only/exclude filter to an event. An empty only list
// admits every event; a non-empty only list requires a match. Exclude always
// wins: a matching exclude entry drops the event regardless of only.
func passesFilter(kind, eventKey string, only, exclude []string) bool {
	if len(only) > 0 && !eventMatches(kind, eventKey, only) {
		return false
	}
	if eventMatches(kind, eventKey, exclude) {
		return false
	}
	return true
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

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	view := normalizeGitlab(raw)
	vlog.V1("gitlab: received %s (eventKey: %s)", view.Kind, view.EventKey)

	// Apply the only/exclude filter before doing any work.
	if !passesFilter(view.Kind, view.EventKey, s.gitCfg.Only, s.gitCfg.Exclude) {
		vlog.V2("gitlab: %s filtered out -> ignored", view.EventKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gitlabIgnoredResponse{OK: true, Ignored: true, Event: view.EventKey})
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

	vlog.V1("gitlab: sent %s to %s -> 200 (%dms)", view.EventKey, targetChat, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
}

// gitlabIgnoredResponse is returned with 200 OK when an event is filtered out
// by the only/exclude rules and no message is sent.
type gitlabIgnoredResponse struct {
	OK      bool   `json:"ok"`
	Ignored bool   `json:"ignored"`
	Event   string `json:"event"`
}

// DefaultGitlabTemplate is a generic fallback that renders any GitLab event.
// Per-event templates layer on top of this in later work.
const DefaultGitlabTemplate = `{{ .EventKey }}{{ if .Project }} [{{ .Project }}]{{ end }}{{ if .Title }}
{{ .Title }}{{ end }}{{ if .User }}
  Автор: {{ .User }}{{ end }}{{ if .URL }}
{{ .URL }}{{ end }}`

// gitlabFuncMap returns the template helpers available to GitLab templates.
// `get` reaches an arbitrary dotted path inside the raw payload:
// `{{ get .Raw "project.web_url" }}`.
func gitlabFuncMap() template.FuncMap {
	return template.FuncMap{
		"get": func(m map[string]any, path string) any {
			return gitlabNestedGet(m, path)
		},
	}
}

// ParseGitlabTemplate compiles a Go text/template for GitLab messages, with the
// GitLab helper funcmap attached.
func ParseGitlabTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("gitlab").Funcs(gitlabFuncMap()).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing gitlab template: %w", err)
	}
	return t, nil
}
