package server

// The GitLab handler implements a reusable "generic webhook" pattern that other
// event-source handlers (any provider that emits many, evolving event types) can
// follow instead of hardcoding a struct per event:
//
//  1. Generic decode: unmarshal the body into map[string]any rather than a typed
//     struct, so new/unknown event shapes never require Go changes.
//  2. Event key: reduce the payload to a stable "kind" or "kind.subtype" string
//     (deriveEventKey) and a best-effort normalized view (normalizeGitlab) with a
//     `get "a.b.c"` template helper for arbitrary nested access into .Raw.
//  3. Config filter: allow/deny events by key with wildcard matching
//     (eventMatches / passesFilter) — only + exclude, exclude wins.
//  4. Template registry: select a template by exact key, then bare kind, then a
//     guaranteed generic default (gitlabTemplates) so every event always renders.
//  5. Status mapping: reuse the same key matcher against error_events to set the
//     BotX notification status (ok/error).
//
// See docs/integrations.md ("GitLab") for the user-facing description.

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
	// Templates is the compiled registry of per-event templates plus a generic
	// default. It selects a template by event key, falling back to the bare kind
	// and finally the default so every event renders.
	Templates *gitlabTemplates
	// FallbackChatID is resolved at startup from the config's chats section
	// when there is exactly one chat alias configured. Empty otherwise.
	FallbackChatID string
	// Only and Exclude filter incoming events by event key. Each entry matches
	// a full event key ("kind.subtype"), a bare kind (all subtypes), or a
	// "kind.*" wildcard. An empty Only allows every event; Exclude always wins.
	Only    []string
	Exclude []string
	// ErrorEvents lists event keys that should be delivered with status "error"
	// (surfaced as BotX notification.status). Entries use the same matching as
	// Only/Exclude (full key, bare kind, or "kind.*"). Everything else is "ok".
	ErrorEvents []string
	// Routes is the compiled, ordered routing rule list. When non-empty (and no
	// explicit ?chat_id overrides it) an incoming event fans out to the chats of
	// every matching rule (see gitlab_routing.go). Empty keeps the single-chat
	// behaviour (DefaultChatID / FallbackChatID).
	Routes []compiledRoute
	// Senders optionally maps additional X-Gitlab-Token values to isolated
	// per-team chat scopes. Secrets are already resolved (env:/vault: references
	// expanded at startup). A request that authenticates via a sender token is
	// delivered only to that sender's Chats; ?chat_id, Routes and DefaultChatID
	// do not apply to it. May coexist with the default SecretToken.
	Senders []GitlabSender
}

// GitlabSender is one isolated webhook sender: a resolved secret token bound to
// a fixed set of target chats (aliases or UUIDs).
type GitlabSender struct {
	Secret string
	Chats  []string
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
//	otherwise            -> object_attributes.action, then object_attributes.status,
//	                        then a flat top-level status (e.g. deployment)
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
		} else if s := gitlabStringAt(raw, "object_attributes.status"); s != "" {
			subtype = s
		} else {
			// Some hooks (e.g. deployment) carry a flat top-level status
			// field rather than object_attributes.status.
			subtype = gitlabStringAt(raw, "status")
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

	// Verify X-Gitlab-Token (GitLab cannot set Authorization/X-API-Key). The
	// token may be the default SecretToken or one of the per-sender secrets;
	// a sender match carries an isolated chat scope (used in delivery below).
	senderChats, isSender, ok := s.resolveGitlabAuth(r.Header.Get("X-Gitlab-Token"))
	if !ok {
		vlog.V1("gitlab: token mismatch -> 401")
		writeError(w, http.StatusUnauthorized, "invalid gitlab token")
		return
	}
	if isSender {
		vlog.V2("gitlab: authenticated as sender (scope: %v)", senderChats)
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

	// Render template: exact event key -> bare kind -> generic default.
	message, err := s.gitCfg.Templates.Render(view.Kind, view.EventKey, view)
	if err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}
	vlog.V3("gitlab: rendered message:\n%s", message)

	// Never post a blank message (e.g. a payload without object_kind that
	// renders to an empty generic template). Treat it as ignored rather than
	// delivering an empty notification to the chat.
	if strings.TrimSpace(message) == "" {
		vlog.V2("gitlab: %s rendered empty message -> ignored", view.EventKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gitlabIgnoredResponse{OK: true, Ignored: true, Event: view.EventKey})
		return
	}

	// Map to BotX notification.status: error when the event key matches
	// ErrorEvents, otherwise ok. Shared by the single-chat and fan-out paths.
	status := "ok"
	if eventMatches(view.Kind, view.EventKey, s.gitCfg.ErrorEvents) {
		status = "error"
	}

	// A sender-token request is delivered only to that sender's chat scope
	// (team isolation): ?chat_id, ?bot, Routes and DefaultChatID do not apply.
	// The filter/template/status logic above is shared with the default path.
	if isSender {
		s.gitlabFanout(w, r, "", senderChats, message, status, view.EventKey)
		return
	}

	// An explicit ?chat_id overrides routing entirely; likewise, with no routes
	// configured the endpoint keeps its original single-chat behaviour (routes is
	// optional, so its absence must not change existing deployments).
	queryChat := r.URL.Query().Get("chat_id")
	if queryChat != "" || len(s.gitCfg.Routes) == 0 {
		targetChat := queryChat
		if targetChat == "" {
			targetChat = s.singleGitlabChat()
		}
		if targetChat == "" {
			writeError(w, http.StatusBadRequest, "chat_id is required: set default_chat_id in config, configure a single chat alias, or pass ?chat_id=")
			return
		}
		s.gitlabSendSingle(w, r, targetChat, message, status, view.EventKey)
		return
	}

	// Routing engine: fan the event out to the chats of every matching rule. When
	// no rule matches, fall back to the single default chat; if there is none the
	// event is ignored (200) rather than treated as an error.
	targets, matched := evaluateRoutes(s.gitCfg.Routes, view)
	if !matched {
		if single := s.singleGitlabChat(); single != "" {
			targets = []string{single}
		} else {
			vlog.V2("gitlab: %s matched no route and no default chat -> ignored", view.EventKey)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(gitlabIgnoredResponse{OK: true, Ignored: true, Event: view.EventKey})
			return
		}
	}
	s.gitlabFanout(w, r, r.URL.Query().Get("bot"), targets, message, status, view.EventKey)
}

// resolveGitlabAuth authenticates an incoming X-Gitlab-Token value against the
// per-sender secrets and the default SecretToken. Every configured secret is
// compared with subtle.ConstantTimeCompare and no comparison is skipped after a
// match, so match position does not affect response timing. (ConstantTimeCompare
// itself returns immediately on a length mismatch, so the timing of a single
// comparison can still reflect whether the token length matches a secret —
// standard and acceptable for webhook tokens.)
// Empty tokens and empty secrets never authenticate, so a misconfigured empty
// secret cannot let an unauthenticated request through. A sender match returns
// that sender's isolated chat scope with isSender=true; a default-token match
// returns (nil, false, true); no match returns (nil, false, false). A token
// matching both a sender and the default resolves as the sender (startup
// deduplication in buildGitlabConfig rejects such configs anyway).
func (s *Server) resolveGitlabAuth(token string) (chats []string, isSender, ok bool) {
	if token == "" {
		return nil, false, false
	}
	tokenBytes := []byte(token)
	var matched *GitlabSender
	for i := range s.gitCfg.Senders {
		sender := &s.gitCfg.Senders[i]
		hit := sender.Secret != "" &&
			subtle.ConstantTimeCompare(tokenBytes, []byte(sender.Secret)) == 1
		if hit && matched == nil {
			matched = sender
		}
	}
	defaultHit := s.gitCfg.SecretToken != "" &&
		subtle.ConstantTimeCompare(tokenBytes, []byte(s.gitCfg.SecretToken)) == 1
	if matched != nil {
		return matched.Chats, true, true
	}
	if defaultHit {
		return nil, false, true
	}
	return nil, false, false
}

// singleGitlabChat returns the single fallback delivery chat, following the
// precedence default_chat_id -> global default chat -> the sole configured chat
// alias. It is empty when none is configured.
func (s *Server) singleGitlabChat() string {
	if s.gitCfg.DefaultChatID != "" {
		return s.gitCfg.DefaultChatID
	}
	if s.cfg.DefaultChatAlias != "" {
		return s.cfg.DefaultChatAlias
	}
	return s.gitCfg.FallbackChatID
}

// gitlabSendSingle delivers a rendered event to exactly one chat, preserving the
// endpoint's original response shape (sendResponse) and status codes: 400 on a
// chat/bot resolution error, 502 on an upstream send failure, and 200 with the
// sync_id on success. It backs the ?chat_id override and the no-routes default.
func (s *Server) gitlabSendSingle(w http.ResponseWriter, r *http.Request, targetChat, message, status, eventKey string) {
	chatResult, err := s.chats(targetChat)
	if err != nil {
		writeError(w, http.StatusBadRequest, "resolving chat: "+err.Error())
		return
	}
	// Resolve bot: explicit ?bot= > chat-bound bot > auth bot.
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
	if err != nil {
		vlog.V1("gitlab: send failed -> 502 (%dms)", elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	vlog.V1("gitlab: sent %s to %s -> 200 (%dms)", eventKey, targetChat, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
}

// gitlabFanout delivers a rendered event to every target chat best-effort,
// resolving chat and bot per target and collecting successes and failures
// independently. It responds 200 with the results (plus any partial errors) when
// at least one delivery succeeds, or 502 with the errors when they all fail.
// requestBot is the ?bot= override; the sender-isolated path passes "" so a
// sender token cannot pick another configured bot's identity.
func (s *Server) gitlabFanout(w http.ResponseWriter, r *http.Request, requestBot string, targets []string, message, status, eventKey string) {
	var results []gitlabFanoutResult
	var errs []gitlabFanoutError
	start := time.Now()
	for _, target := range targets {
		chatResult, err := s.chats(target)
		if err != nil {
			errs = append(errs, gitlabFanoutError{Chat: target, Error: "resolving chat: " + err.Error()})
			continue
		}
		botName, errMsg := s.resolveRequestBot(r.Context(), requestBot, chatResult.Bot)
		if errMsg != "" {
			errs = append(errs, gitlabFanoutError{Chat: target, Error: errMsg})
			continue
		}
		syncID, err := s.send(r.Context(), &SendPayload{
			Bot:     botName,
			ChatID:  chatResult.ChatID,
			Message: message,
			Status:  status,
		})
		if err != nil {
			errs = append(errs, gitlabFanoutError{Chat: target, Error: err.Error()})
			continue
		}
		results = append(results, gitlabFanoutResult{Chat: target, SyncID: syncID})
	}
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	if len(results) == 0 {
		vlog.V1("gitlab: %s fan-out to %d chats all failed -> 502 (%dms)", eventKey, len(targets), elapsed.Milliseconds())
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(gitlabFanoutResponse{OK: false, Errors: errs})
		return
	}
	vlog.V1("gitlab: %s fan-out delivered to %d/%d chats -> 200 (%dms)", eventKey, len(results), len(targets), elapsed.Milliseconds())
	json.NewEncoder(w).Encode(gitlabFanoutResponse{OK: true, Results: results, Errors: errs})
}

// gitlabIgnoredResponse is returned with 200 OK when an event is filtered out
// by the only/exclude rules and no message is sent.
type gitlabIgnoredResponse struct {
	OK      bool   `json:"ok"`
	Ignored bool   `json:"ignored"`
	Event   string `json:"event"`
}

// gitlabFanoutResponse is the routing endpoint's response when routes are
// configured: a best-effort fan-out that reports each successful delivery in
// results and each failed one in errors. OK is true when at least one delivery
// succeeded (HTTP 200); it is false when they all failed (HTTP 502).
type gitlabFanoutResponse struct {
	OK      bool                 `json:"ok"`
	Results []gitlabFanoutResult `json:"results,omitempty"`
	Errors  []gitlabFanoutError  `json:"errors,omitempty"`
}

// gitlabFanoutResult is a single successful fan-out delivery: the target chat
// (alias or UUID as configured in the rule) and the BotX sync_id.
type gitlabFanoutResult struct {
	Chat   string `json:"chat"`
	SyncID string `json:"sync_id"`
}

// gitlabFanoutError is a single failed fan-out delivery: the target chat and the
// error that prevented delivery (chat/bot resolution or the upstream send).
type gitlabFanoutError struct {
	Chat  string `json:"chat"`
	Error string `json:"error"`
}

// DefaultGitlabTemplate is the generic fallback that renders any GitLab event
// for which no more specific template exists. It is registered under the
// "default" key of DefaultGitlabTemplates.
const DefaultGitlabTemplate = `{{ .EventKey }}{{ if .Project }} [{{ .Project }}]{{ end }}{{ if .Title }}
{{ .Title }}{{ end }}{{ if .User }}
  Автор: {{ .User }}{{ end }}{{ if .URL }}
{{ .URL }}{{ end }}`

// DefaultGitlabTemplates are the built-in templates keyed by event key. They
// cover the most common events and always include a "default" fallback. Any key
// can be overridden via configuration; the "default" key becomes the registry's
// generic fallback for unrecognised events.
var DefaultGitlabTemplates = map[string]string{
	"default": DefaultGitlabTemplate,

	"merge_request.open":  "🆕 MR{{ if .Project }} [{{ .Project }}]{{ end }}: {{ .Title }}\n  Автор: {{ .User }}\n{{ .URL }}",
	"merge_request.merge": "✅ MR смёржен{{ if .Project }} [{{ .Project }}]{{ end }}: {{ .Title }}\n  Автор: {{ .User }}\n{{ .URL }}",
	"merge_request.close": "🚫 MR закрыт{{ if .Project }} [{{ .Project }}]{{ end }}: {{ .Title }}\n  Автор: {{ .User }}\n{{ .URL }}",

	"note.MergeRequest": "💬 Комментарий к MR{{ if .Project }} [{{ .Project }}]{{ end }} от {{ .User }}{{ with get .Raw \"object_attributes.note\" }}\n{{ . }}{{ end }}\n{{ .URL }}",

	"push":     "⬆️ Push{{ if .Project }} [{{ .Project }}]{{ end }} от {{ .User }}{{ with get .Raw \"ref\" }}\n  Ветка: {{ . }}{{ end }}{{ if .URL }}\n{{ .URL }}{{ end }}",
	"tag_push": "🏷️ Tag push{{ if .Project }} [{{ .Project }}]{{ end }} от {{ .User }}{{ with get .Raw \"ref\" }}\n  Реф: {{ . }}{{ end }}{{ if .URL }}\n{{ .URL }}{{ end }}",

	"pipeline": "🚦 Pipeline{{ if .Project }} [{{ .Project }}]{{ end }}: {{ .Action }}{{ if .URL }}\n{{ .URL }}{{ end }}",

	"issue": "📌 Issue{{ if .Project }} [{{ .Project }}]{{ end }}: {{ .Title }}\n  Автор: {{ .User }}{{ if .URL }}\n{{ .URL }}{{ end }}",
}

// gitlabTemplates is the compiled template registry: per-event templates keyed
// by event key (or bare kind) plus a mandatory generic default.
type gitlabTemplates struct {
	byKey map[string]*template.Template
	def   *template.Template
}

// gitlabFuncMap returns the template helpers available to GitLab templates.
//
//	get   reaches an arbitrary dotted path inside the raw payload:
//	      {{ get .Raw "project.web_url" }} (nil when absent)
//	default returns its first argument when the second is empty/nil:
//	      {{ default "n/a" .Title }}
func gitlabFuncMap() template.FuncMap {
	return template.FuncMap{
		"get": func(m map[string]any, path string) any {
			return gitlabNestedGet(m, path)
		},
		"default": func(dflt, val any) any {
			if gitlabIsEmpty(val) {
				return dflt
			}
			return val
		},
	}
}

// gitlabIsEmpty reports whether a template value is considered empty for the
// `default` helper: nil or the empty string.
func gitlabIsEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	default:
		return false
	}
}

// ParseGitlabTemplates compiles the GitLab template registry. It starts from the
// built-in DefaultGitlabTemplates and overlays the caller-supplied inline
// templates (a user entry replaces a default of the same key). Every template is
// compiled with the GitLab helper funcmap; a parse error aborts startup. The
// "default" key is always present and becomes the generic fallback.
func ParseGitlabTemplates(inline map[string]string) (*gitlabTemplates, error) {
	// Reject ambiguous catch-alls up front: the bare "kind" and "kind.*" forms
	// canonicalise to the same registry slot, so supplying both leaves the winner
	// to nondeterministic map iteration below. Config.Validate flags this too, but
	// that check does not run on serve startup, so enforce it here on the shared
	// code path. Iterate in sorted order for a stable error message.
	seen := make(map[string]string, len(inline))
	inlineKeys := make([]string, 0, len(inline))
	for k := range inline {
		inlineKeys = append(inlineKeys, k)
	}
	sort.Strings(inlineKeys)
	for _, k := range inlineKeys {
		canon := canonGitlabTemplateKey(k)
		if prev, ok := seen[canon]; ok && prev != k {
			return nil, fmt.Errorf("gitlab templates: keys %q and %q are equivalent catch-alls; define only one", prev, k)
		}
		seen[canon] = k
	}

	merged := make(map[string]string, len(DefaultGitlabTemplates)+len(inline))
	for k, v := range DefaultGitlabTemplates {
		merged[canonGitlabTemplateKey(k)] = v
	}
	for k, v := range inline {
		merged[canonGitlabTemplateKey(k)] = v
	}

	gt := &gitlabTemplates{byKey: make(map[string]*template.Template)}
	for k, v := range merged {
		t, err := template.New(k).Funcs(gitlabFuncMap()).Parse(v)
		if err != nil {
			return nil, fmt.Errorf("parsing gitlab template %q: %w", k, err)
		}
		if k == "default" {
			gt.def = t
		} else {
			gt.byKey[k] = t
		}
	}
	if gt.def == nil {
		// DefaultGitlabTemplates always carries "default"; this guards against a
		// caller clearing it in a future refactor.
		return nil, fmt.Errorf("gitlab templates: missing \"default\" template")
	}
	return gt, nil
}

// canonGitlabTemplateKey collapses the wildcard catch-all form "kind.*" to the
// bare kind "kind". The two are equivalent "all subtypes" catch-alls (the same
// way the filter and error_events matchers treat them), so they must share a
// single registry slot: this lets a user template keyed "pipeline.*" override
// the built-in bare "pipeline" default instead of being shadowed by it.
// Ambiguity — a user supplying both forms — is rejected by config validation.
func canonGitlabTemplateKey(key string) string {
	if k, ok := strings.CutSuffix(key, ".*"); ok {
		return k
	}
	return key
}

// selectTemplate picks the template for an event: exact event key, then the bare
// kind catch-all (which subsumes the equivalent kind.* form, canonicalised at
// registration by canonGitlabTemplateKey), then the guaranteed generic default.
func (gt *gitlabTemplates) selectTemplate(kind, eventKey string) *template.Template {
	if t, ok := gt.byKey[eventKey]; ok {
		return t
	}
	if kind != "" {
		if t, ok := gt.byKey[kind]; ok {
			return t
		}
	}
	return gt.def
}

// Render selects the template for kind/eventKey and executes it against data.
func (gt *gitlabTemplates) Render(kind, eventKey string, data any) (string, error) {
	var buf bytes.Buffer
	if err := gt.selectTemplate(kind, eventKey).Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
