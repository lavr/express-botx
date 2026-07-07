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
	"regexp"
	"strings"
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
