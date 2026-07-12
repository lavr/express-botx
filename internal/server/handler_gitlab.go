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
	// Senders contains the complete, isolated runtime for each accepted token.
	// Secrets are resolved and chat scopes/routes normalized during startup.
	Senders []GitlabSender
}

// GitlabSender is one isolated webhook sender: a resolved secret token bound to
// a fixed set of target chats (aliases or UUIDs).
//
// Scope and Targets are two views of the same delivery set and MUST be kept
// consistent by whoever builds the sender: every value in Targets must have a
// Scope entry whose Target equals it, keyed by that chat's canonical UUID. The
// serve builder (internal/cmd) guarantees this; a hand-built sender that fills
// only one view will 403 every ?chat_id request (empty Scope) or fan out to no
// chats (empty Targets).
type GitlabSender struct {
	Label       string // stable log/error label: quoted sender name, or "senders[i]" when unnamed
	Secret      string
	Scope       map[string]GitlabTarget // canonical UUID -> delivery target resolved at build time
	Targets     []string                // ordered, canonical-deduplicated delivery targets
	Only        []string
	Exclude     []string
	ErrorEvents []string
	Templates   *gitlabTemplates
	Routes      []compiledRoute
}

// GitlabTarget is one delivery target of a sender's scope, resolved once at
// build time. Fixing the bot binding here means canonicalizing a request
// reference to the scope target can never silently change the sending bot.
type GitlabTarget struct {
	Target string // configured delivery target (alias or UUID) passed to fan-out
	Bot    string // bot binding of the target alias; "" for raw UUIDs and unbound aliases
}

// BotConflict reports whether delivering a reference ref (bound to refBot) via
// this target would silently switch the sending bot: the reference canonicalizes
// to a different configured target and both sides carry an explicit, differing
// bot binding. An unbound side (raw UUID, or a bot-less alias in single-bot
// mode) cannot conflict. This is the single predicate shared by the request-time
// scope check (senderScopeTargets) and the startup route check (buildGitlabSender
// in internal/cmd); the offline config validator mirrors it.
func (t GitlabTarget) BotConflict(ref, refBot string) bool {
	return ref != t.Target && refBot != "" && t.Bot != "" && refBot != t.Bot
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

	sender, ok := s.resolveGitlabAuth(r.Header.Get("X-Gitlab-Token"))
	if !ok {
		vlog.V1("gitlab: token mismatch -> 401")
		writeError(w, http.StatusUnauthorized, "invalid gitlab token")
		return
	}
	senderLabel := sender.Label // WithGitlab normalizes missing labels at construction
	vlog.V1("gitlab: authenticated sender %s", senderLabel)

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	view := normalizeGitlab(raw)
	vlog.V1("gitlab: received %s (eventKey: %s)", view.Kind, view.EventKey)

	if !passesFilter(view.Kind, view.EventKey, sender.Only, sender.Exclude) {
		vlog.V2("gitlab: sender %s filtered out %s -> ignored", senderLabel, view.EventKey)
		writeGitlabIgnored(w, view.EventKey, "event filtered")
		return
	}

	message, err := sender.Templates.Render(view.Kind, view.EventKey, view)
	if err != nil {
		writeError(w, http.StatusBadRequest, "template error: "+err.Error())
		return
	}
	vlog.V3("gitlab: rendered message:\n%s", message)

	if strings.TrimSpace(message) == "" {
		vlog.V2("gitlab: %s rendered empty message -> ignored", view.EventKey)
		writeGitlabIgnored(w, view.EventKey, "empty message")
		return
	}

	status := "ok"
	if eventMatches(view.Kind, view.EventKey, sender.ErrorEvents) {
		status = "error"
	}

	queryChats := parseChatIDs(r.URL.Query().Get("chat_id"))
	switch {
	case r.URL.Query().Has("chat_id") && len(queryChats) == 0:
		writeError(w, http.StatusBadRequest, "chat_id is empty: provide at least one chat, or omit chat_id")
		return
	case len(queryChats) > 0:
		targets, reject, inScope := s.senderScopeTargets(queryChats, sender.Scope)
		if !inScope {
			vlog.V1("gitlab: sender %s rejected chat selection: %s -> 403", senderLabel, reject)
			writeError(w, http.StatusForbidden, reject)
			return
		}
		vlog.V2("gitlab: sender %s final targets %v", senderLabel, targets)
		s.gitlabDeliver(w, r, "", targets, message, status, view.EventKey)
		return
	case len(sender.Routes) > 0:
		targets, matched, matchedRules := evaluateRoutes(sender.Routes, view)
		if !matched {
			vlog.V1("gitlab: sender %s matched no route for %s -> ignored", senderLabel, view.EventKey)
			writeGitlabIgnored(w, view.EventKey, "no route matched")
			return
		}
		vlog.V2("gitlab: sender %s matched routes %v -> %v", senderLabel, matchedRules, targets)
		s.gitlabDeliver(w, r, "", targets, message, status, view.EventKey)
		return
	default:
		vlog.V2("gitlab: sender %s final targets %v", senderLabel, sender.Targets)
		s.gitlabDeliver(w, r, "", sender.Targets, message, status, view.EventKey)
		return
	}
}

// resolveGitlabAuth authenticates an incoming X-Gitlab-Token value against the
// per-sender secrets. Every configured secret is
// compared with subtle.ConstantTimeCompare and no comparison is skipped after a
// match, so match position does not affect response timing. (ConstantTimeCompare
// itself returns immediately on a length mismatch, so the timing of a single
// comparison can still reflect whether the token length matches a secret —
// standard and acceptable for webhook tokens.)
// Empty tokens and empty secrets never authenticate, so a misconfigured empty
// secret cannot let an unauthenticated request through. On duplicate secrets
// (possible only in hand-built configs; serve startup rejects them) the first
// matching sender wins.
func (s *Server) resolveGitlabAuth(token string) (*GitlabSender, bool) {
	if token == "" {
		return nil, false
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
	return matched, matched != nil
}

// senderScopeTargets canonicalizes requested aliases/UUIDs through the sender's
// prebuilt canonical UUID scope and returns configured delivery targets. It
// rejects the whole request on the first out-of-scope or bot-conflicting value
// (reject carries the client-facing 403 message) and de-duplicates by canonical
// UUID while preserving request order.
func (s *Server) senderScopeTargets(requested []string, scope map[string]GitlabTarget) (targets []string, reject string, ok bool) {
	targets = make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, raw := range requested {
		canonical := raw
		requestedBot := ""
		if _, inScope := scope[canonical]; !inScope {
			resolved, err := s.chats(raw)
			if err != nil || resolved.ChatID == "" {
				// An unresolvable or empty canonical ID is out of scope; ""
				// must never match a scope key (see config.ResolveChatRef).
				return nil, fmt.Sprintf("chat %q is outside this token's allowed chats", raw), false
			}
			canonical = resolved.ChatID
			requestedBot = resolved.Bot
		}
		entry, inScope := scope[canonical]
		if !inScope {
			return nil, fmt.Sprintf("chat %q is outside this token's allowed chats", raw), false
		}
		// Delivery goes through the scope target, so a requested alias carrying
		// a different bot binding would silently switch the sending bot — the
		// same contradiction the startup route check rejects.
		if entry.BotConflict(raw, requestedBot) {
			return nil, fmt.Sprintf("chat %q is bound to bot %q, but this token delivers to that chat via %q (bot %q)",
				raw, requestedBot, entry.Target, entry.Bot), false
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		targets = append(targets, entry.Target)
	}
	return targets, "", true
}

// gitlabDeliver delivers a rendered event to every target chat best-effort using
// the project-wide fan-out primitives (fanout + writeMultiSend). Chat and bot are
// resolved per target and successes/failures are collected independently; the
// response is always a MultiSendResponse — 200 with the results (plus any partial
// errors) when at least one delivery succeeds, or 502 with the errors when they
// all fail. A single target still returns the uniform shape (results[0]), so
// /gitlab shares the contract of every other send surface. The handler passes an
// empty requestBot so sender chat bindings always choose the bot identity.
func (s *Server) gitlabDeliver(w http.ResponseWriter, r *http.Request, requestBot string, targets []string, message, status, eventKey string) {
	start := time.Now()
	results, errs := s.fanoutSend(r.Context(), targets, requestBot, message, status)
	elapsed := time.Since(start)
	if len(results) == 0 {
		vlog.V1("gitlab: %s fan-out to %d chats all failed -> 502 (%dms)", eventKey, len(targets), elapsed.Milliseconds())
	} else {
		vlog.V1("gitlab: %s delivered to %d/%d chats (%dms)", eventKey, len(results), len(targets), elapsed.Milliseconds())
	}
	writeMultiSend(w, results, errs, http.StatusOK)
}

// gitlabIgnoredResponse is returned with 200 OK when an event is accepted but
// no message is sent; Reason names the cause ("event filtered", "empty
// message", "no route matched").
type gitlabIgnoredResponse struct {
	OK      bool   `json:"ok"`
	Ignored bool   `json:"ignored"`
	Event   string `json:"event"`
	Reason  string `json:"reason,omitempty"`
}

// writeGitlabIgnored writes the uniform 200 ignored response shared by the
// filtered, blank-render, and no-route paths.
func writeGitlabIgnored(w http.ResponseWriter, eventKey, reason string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitlabIgnoredResponse{
		OK: true, Ignored: true, Event: eventKey, Reason: reason,
	})
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
