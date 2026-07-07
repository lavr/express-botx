package server

// This file implements the routing engine for GitLab webhook events: a
// configurable set of ordered rules that fan a single incoming event out to one
// or more chats based on the project, event key, branch, and arbitrary payload
// fields. The handler (handler_gitlab.go) owns decode/normalize/render; this
// file owns "which chats does this event go to".
//
// The pieces build up in layers:
//
//  1. Pattern matchers (this file, Task 1): a value-vs-pattern predicate.
//     Patterns are globs by default (a simple `*` wildcard) or `/regex/` for a
//     Go RE2 regular expression. The special "event" selector does not use these
//     patterns at all: it matches through eventMatches (kind / kind.subtype /
//     kind.* semantics), so glob/regex are never applied to it.
//  2. Match context (Task 2): resolve a selector (reserved key or dotted raw
//     path) to the set of candidate string values for an event.
//  3. Rules + evaluate (Task 3): AND across conditions, OR within a condition,
//     collect + dedup the chats of every matching rule, stop on `stop:true`.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lavr/express-botx/internal/config"
)

// patternMatcher tests a single string value against a compiled pattern. glob
// and regex patterns both satisfy it; the "event" selector is handled
// separately via eventMatcher because its matching is not value-based.
type patternMatcher interface {
	matches(value string) bool
}

// globMatcher matches with a simple `*` wildcard: `*` stands for any run of
// characters (including none and including "/"), everything else is literal.
// A pattern without `*` is an exact-equality check.
type globMatcher struct {
	pattern string
}

func (g globMatcher) matches(value string) bool {
	return globMatch(g.pattern, value)
}

// globMatch reports whether value matches a `*`-glob pattern. Multiple `*` are
// supported: the pattern is split on `*` into literal segments that must appear
// in order, with the first anchored to the start and the last to the end.
func globMatch(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		// No wildcard: exact match.
		return pattern == value
	}
	// The first segment must be a prefix of value.
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]
	// Interior segments must appear in order, each consuming the earliest match.
	for _, mid := range parts[1 : len(parts)-1] {
		idx := strings.Index(value, mid)
		if idx < 0 {
			return false
		}
		value = value[idx+len(mid):]
	}
	// The last segment must be a suffix of what remains (empty when the pattern
	// ends with `*`, which always matches).
	return strings.HasSuffix(value, parts[len(parts)-1])
}

// regexMatcher matches with a compiled Go (RE2) regular expression. The pattern
// is unanchored: it matches if the expression is found anywhere in the value,
// so callers anchor with ^/$ when they want a full match.
type regexMatcher struct {
	re *regexp.Regexp
}

func (r regexMatcher) matches(value string) bool {
	return r.re.MatchString(value)
}

// compilePattern builds a patternMatcher from a config pattern string. A pattern
// wrapped in slashes ("/.../"), with at least the two delimiters, is compiled as
// a Go RE2 regular expression; anything else is a `*`-glob. A malformed regular
// expression returns an error so it fails fast at config-compile time.
func compilePattern(pattern string) (patternMatcher, error) {
	if len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
		expr := pattern[1 : len(pattern)-1]
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("compiling regex pattern %q: %w", pattern, err)
		}
		return regexMatcher{re: re}, nil
	}
	return globMatcher{pattern: pattern}, nil
}

// eventMatcher matches the special "event" selector. Its entries use the same
// key semantics as the only/exclude filter (full key "kind.subtype", a bare
// "kind" for every subtype, or the "kind.*" wildcard) via eventMatches. glob and
// regex patterns are deliberately not applied to event selectors.
type eventMatcher []string

func (m eventMatcher) matchesEvent(kind, eventKey string) bool {
	return eventMatches(kind, eventKey, []string(m))
}

// --- Task 2: match context (reserved keys, raw dotted paths, branch) ---

// gitlabBranch derives a normalized branch name for an event, straight from the
// raw payload so gitlabView does not need a Branch field (keeping this engine
// decoupled from the shared view model). The source depends on the kind:
//
//	merge_request, note -> object_attributes.target_branch
//	                       (fallback merge_request.target_branch)
//	push, tag_push      -> the branch of ref ("refs/heads/main" -> "main")
//	pipeline            -> object_attributes.ref
//	build (job)         -> the branch of the top-level ref (bare "main")
//	otherwise           -> "" (issue, wiki, deployment, ... have no branch)
//
// Non-MR notes (on a commit/issue/snippet) carry no target_branch, so they
// resolve to "" as well.
func gitlabBranch(kind string, raw map[string]any) string {
	switch kind {
	case "merge_request", "note":
		if b := gitlabStringAt(raw, "object_attributes.target_branch"); b != "" {
			return b
		}
		return gitlabStringAt(raw, "merge_request.target_branch")
	case "push", "tag_push", "build":
		return refBranch(gitlabStringAt(raw, "ref"))
	case "pipeline":
		return gitlabStringAt(raw, "object_attributes.ref")
	default:
		return ""
	}
}

// gitlabProject resolves the routing value for the reserved "project" selector.
// It prefers the namespaced path (project.path_with_namespace) so namespaced
// globs like "group/backend/*" match, falling back to the short project.name.
// This is intentionally decoupled from gitlabView.Project (which prefers the
// short name for compact display in message templates): routing needs the
// namespace, templates want brevity.
func gitlabProject(raw map[string]any) string {
	if p := gitlabStringAt(raw, "project.path_with_namespace"); p != "" {
		return p
	}
	return gitlabStringAt(raw, "project.name")
}

// refBranch reduces a Git ref to its branch/tag name. The conventional
// "refs/heads/" and "refs/tags/" prefixes are stripped while preserving any
// interior slashes (so "refs/heads/release/2.0" -> "release/2.0", which still
// matches a "release/*" glob); an unrecognised ref falls back to its basename.
func refBranch(ref string) string {
	if ref == "" {
		return ""
	}
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		if s, ok := strings.CutPrefix(ref, prefix); ok {
			return s
		}
	}
	return path.Base(ref)
}

// resolveSelector maps a match selector to the candidate string values it should
// be tested against for this event. A reserved selector name yields the
// corresponding normalized field (empty -> no values); any other selector is a
// dotted path into the raw payload (scalar -> one value, array -> one value per
// scalar element, missing -> no values). The "event" selector resolves to the
// event key here for completeness, but rule evaluation matches it through the
// dedicated event matcher rather than value patterns.
func resolveSelector(view gitlabView, selector string) []string {
	switch selector {
	case "kind":
		return nonEmpty(view.Kind)
	case "event":
		return nonEmpty(view.EventKey)
	case "action":
		return nonEmpty(view.Action)
	case "project":
		return nonEmpty(gitlabProject(view.Raw))
	case "branch":
		return nonEmpty(gitlabBranch(view.Kind, view.Raw))
	case "user":
		return nonEmpty(view.User)
	case "title":
		return nonEmpty(view.Title)
	case "url":
		return nonEmpty(view.URL)
	default:
		return gitlabSelectorStrings(gitlabNestedGet(view.Raw, selector))
	}
}

// nonEmpty wraps a single reserved-field value: one value when non-empty, none
// when empty (an absent field must not match any pattern).
func nonEmpty(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

// gitlabSelectorStrings flattens a raw payload value into matchable strings. A
// scalar (string/number/bool) yields a single string; an array yields one string
// per scalar element (non-scalar elements, e.g. label objects, are skipped);
// anything else (nil, a nested object) yields no values.
func gitlabSelectorStrings(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := gitlabScalarString(e); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		if s, ok := gitlabScalarString(v); ok {
			return []string{s}
		}
		return nil
	}
}

// gitlabScalarString renders a JSON scalar as a string for pattern matching.
// Numbers decode as float64 and are formatted without a trailing ".0"; objects
// and arrays are not scalars and report false.
func gitlabScalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	default:
		return "", false
	}
}

// conditionMatches reports whether any candidate value matches any matcher
// (OR within a condition, both across values and across patterns). The "event"
// selector does not use this path; rule evaluation matches it separately.
func conditionMatches(values []string, matchers []patternMatcher) bool {
	for _, v := range values {
		for _, m := range matchers {
			if m.matches(v) {
				return true
			}
		}
	}
	return false
}

// --- Task 3: rules and evaluate (all-match + stop + dedup) ---

// compiledCondition is one selector predicate of a compiled route. For every
// selector but "event" the candidate values (resolveSelector) are tested against
// matchers with OR semantics; when selector == "event" the event field carries an
// eventMatcher and value patterns are not used.
type compiledCondition struct {
	selector string
	matchers []patternMatcher
	event    eventMatcher // set only when selector == "event"
}

// matches reports whether this condition holds for an event. The "event" selector
// routes through the dedicated event matcher (kind / kind.subtype / kind.*);
// every other selector resolves candidate values and OR-matches them against the
// condition's patterns.
func (c compiledCondition) matches(view gitlabView) bool {
	if c.selector == "event" {
		return c.event.matchesEvent(view.Kind, view.EventKey)
	}
	return conditionMatches(resolveSelector(view, c.selector), c.matchers)
}

// compiledRoute is a single routing rule: a set of conditions (AND), the chats a
// matching event fans out to, and whether a match stops the rule scan.
type compiledRoute struct {
	conds []compiledCondition
	chats []string
	stop  bool
}

// routeMatches reports whether a route applies to an event. All conditions must
// hold (AND across fields); a selector that the rule does not mention is simply
// absent from conds and so places no constraint. A route with no conditions is a
// catch-all that always matches.
func routeMatches(view gitlabView, route compiledRoute) bool {
	for _, cond := range route.conds {
		if !cond.matches(view) {
			return false
		}
	}
	return true
}

// CompileGitlabRoutes turns the YAML routing rules into the compiled form the
// engine evaluates, compiling every condition's patterns up front so a malformed
// regex fails at serve startup rather than per-request. Rule order is preserved;
// within a rule the selectors are compiled in a stable (sorted) order, which does
// not affect matching (AND across selectors) but keeps the compiled form
// deterministic. The "event" selector carries an eventMatcher (kind / kind.subtype
// / kind.* semantics); every other selector compiles its patterns via
// compilePattern (glob or "/regex/"). An empty input yields a nil slice, which
// keeps the endpoint's single-chat behaviour.
func CompileGitlabRoutes(routes []config.GitlabRouteYAMLConfig) ([]compiledRoute, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	compiled := make([]compiledRoute, 0, len(routes))
	for i, r := range routes {
		cr := compiledRoute{chats: r.Chats, stop: r.Stop}
		selectors := make([]string, 0, len(r.Match))
		for selector := range r.Match {
			selectors = append(selectors, selector)
		}
		sort.Strings(selectors)
		for _, selector := range selectors {
			patterns := r.Match[selector]
			if selector == "event" {
				cr.conds = append(cr.conds, compiledCondition{
					selector: "event",
					event:    eventMatcher(patterns),
				})
				continue
			}
			matchers := make([]patternMatcher, 0, len(patterns))
			for _, p := range patterns {
				m, err := compilePattern(p)
				if err != nil {
					return nil, fmt.Errorf("routes[%d].match.%s: %w", i, selector, err)
				}
				matchers = append(matchers, m)
			}
			cr.conds = append(cr.conds, compiledCondition{
				selector: selector,
				matchers: matchers,
			})
		}
		compiled = append(compiled, cr)
	}
	return compiled, nil
}

// evaluateRoutes runs the ordered rule list against an event. Every matching rule
// contributes its chats (all-match, not first-match); the chats are unioned with
// duplicates removed while preserving first-seen order. A matching rule with
// stop:true ends the scan after its chats are collected. matched reports whether
// any rule matched at all (distinct from "matched but produced no chats", which a
// well-formed config never does since chats is required non-empty).
func evaluateRoutes(routes []compiledRoute, view gitlabView) (chats []string, matched bool) {
	seen := make(map[string]struct{})
	for _, route := range routes {
		if !routeMatches(view, route) {
			continue
		}
		matched = true
		for _, chat := range route.chats {
			if _, ok := seen[chat]; ok {
				continue
			}
			seen[chat] = struct{}{}
			chats = append(chats, chat)
		}
		if route.stop {
			break
		}
	}
	return chats, matched
}
