package server

import (
	"reflect"
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

func TestCompilePatternGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"exact match", "group/backend", "group/backend", true},
		{"exact no match", "group/backend", "group/frontend", false},
		{"trailing star matches child", "group/backend/*", "group/backend/api", true},
		{"trailing star matches nested", "group/backend/*", "group/backend/api/v1", true},
		{"trailing star matches empty", "group/backend/*", "group/backend/", true},
		{"trailing star no match other prefix", "group/backend/*", "group/frontend/api", false},
		{"prefix star", "sec:*", "sec:alert", true},
		{"prefix star no match", "sec:*", "ops:alert", false},
		{"leading star", "*.example.com", "a.b.example.com", true},
		{"leading star no match", "*.example.com", "example.org", false},
		{"middle star", "release/*/hotfix", "release/2.0/hotfix", true},
		{"middle star no match", "release/*/hotfix", "release/2.0/feature", false},
		{"multiple stars", "a*b*c", "axxbyyc", true},
		{"multiple stars no match", "a*b*c", "axxc", false},
		{"lone star matches anything", "*", "anything/at/all", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := compilePattern(tt.pattern)
			if err != nil {
				t.Fatalf("compilePattern(%q) error: %v", tt.pattern, err)
			}
			if got := m.matches(tt.value); got != tt.want {
				t.Errorf("matches(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCompilePatternRegex(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"anchored release branch", `/^release\//`, "release/2.0", true},
		{"anchored no match", `/^release\//`, "feature/x", false},
		{"unanchored substring", `/hotfix/`, "release/hotfix/urgent", true},
		{"alternation", `/^(main|master)$/`, "master", true},
		{"alternation no match", `/^(main|master)$/`, "develop", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := compilePattern(tt.pattern)
			if err != nil {
				t.Fatalf("compilePattern(%q) error: %v", tt.pattern, err)
			}
			if _, ok := m.(regexMatcher); !ok {
				t.Fatalf("compilePattern(%q) = %T, want regexMatcher", tt.pattern, m)
			}
			if got := m.matches(tt.value); got != tt.want {
				t.Errorf("matches(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCompilePatternBrokenRegex(t *testing.T) {
	if _, err := compilePattern(`/[unterminated/`); err == nil {
		t.Fatal("expected error compiling malformed regex, got nil")
	}
}

func TestCompilePatternSlashGlobFallback(t *testing.T) {
	// A single "/" is not a regex delimiter pair; it stays a literal glob.
	m, err := compilePattern("/")
	if err != nil {
		t.Fatalf("compilePattern error: %v", err)
	}
	if _, ok := m.(globMatcher); !ok {
		t.Fatalf("compilePattern(%q) = %T, want globMatcher", "/", m)
	}
	if !m.matches("/") {
		t.Errorf("literal glob %q should match itself", "/")
	}
}

func TestEventMatcher(t *testing.T) {
	tests := []struct {
		name     string
		entries  []string
		kind     string
		eventKey string
		want     bool
	}{
		{"full key match", []string{"merge_request.open"}, "merge_request", "merge_request.open", true},
		{"full key no match", []string{"merge_request.open"}, "merge_request", "merge_request.close", false},
		{"bare kind matches any subtype", []string{"pipeline"}, "pipeline", "pipeline.failed", true},
		{"kind.* wildcard matches subtype", []string{"pipeline.*"}, "pipeline", "pipeline.success", true},
		{"kind.* wildcard no match other kind", []string{"pipeline.*"}, "push", "push", false},
		{"multiple entries any match", []string{"push", "merge_request.open"}, "push", "push", true},
		{"no entries never matches", nil, "push", "push", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := eventMatcher(tt.entries)
			if got := m.matchesEvent(tt.kind, tt.eventKey); got != tt.want {
				t.Errorf("matchesEvent(%q, %q) = %v, want %v", tt.kind, tt.eventKey, got, tt.want)
			}
		})
	}
}

func TestGitlabBranch(t *testing.T) {
	tests := []struct {
		name string
		kind string
		raw  map[string]any
		want string
	}{
		{
			name: "merge_request target_branch",
			kind: "merge_request",
			raw:  map[string]any{"object_attributes": map[string]any{"target_branch": "main"}},
			want: "main",
		},
		{
			name: "note on MR falls back to merge_request.target_branch",
			kind: "note",
			raw:  map[string]any{"merge_request": map[string]any{"target_branch": "develop"}},
			want: "develop",
		},
		{
			name: "note on commit has no branch",
			kind: "note",
			raw:  map[string]any{"object_attributes": map[string]any{"noteable_type": "Commit"}},
			want: "",
		},
		{
			name: "push strips refs/heads prefix",
			kind: "push",
			raw:  map[string]any{"ref": "refs/heads/main"},
			want: "main",
		},
		{
			name: "push preserves interior slashes",
			kind: "push",
			raw:  map[string]any{"ref": "refs/heads/release/2.0"},
			want: "release/2.0",
		},
		{
			name: "tag_push strips refs/tags prefix",
			kind: "tag_push",
			raw:  map[string]any{"ref": "refs/tags/v1.2.3"},
			want: "v1.2.3",
		},
		{
			name: "pipeline uses object_attributes.ref",
			kind: "pipeline",
			raw:  map[string]any{"object_attributes": map[string]any{"ref": "feature/x"}},
			want: "feature/x",
		},
		{
			name: "build uses top-level bare ref",
			kind: "build",
			raw:  map[string]any{"ref": "main"},
			want: "main",
		},
		{
			name: "build strips refs/heads prefix when present",
			kind: "build",
			raw:  map[string]any{"ref": "refs/heads/release/2.0"},
			want: "release/2.0",
		},
		{
			name: "build preserves namespace of a bare slashed branch",
			kind: "build",
			raw:  map[string]any{"ref": "feature/login"},
			want: "feature/login",
		},
		{
			name: "issue has no branch",
			kind: "issue",
			raw:  map[string]any{"object_attributes": map[string]any{"title": "bug"}},
			want: "",
		},
		{
			name: "push with unknown ref shape falls back to basename",
			kind: "push",
			raw:  map[string]any{"ref": "weird/thing"},
			want: "thing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitlabBranch(tt.kind, tt.raw); got != tt.want {
				t.Errorf("gitlabBranch(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestResolveSelectorReserved(t *testing.T) {
	view := gitlabView{
		Kind:     "merge_request",
		Action:   "open",
		EventKey: "merge_request.open",
		Project:  "group/backend",
		User:     "alice",
		Title:    "Add feature",
		URL:      "https://example.com/mr/1",
		Raw: map[string]any{
			"project":           map[string]any{"path_with_namespace": "group/backend"},
			"object_attributes": map[string]any{"target_branch": "main"},
		},
	}
	tests := []struct {
		selector string
		want     []string
	}{
		{"kind", []string{"merge_request"}},
		{"event", []string{"merge_request.open"}},
		{"action", []string{"open"}},
		{"project", []string{"group/backend"}},
		{"branch", []string{"main"}},
		{"user", []string{"alice"}},
		{"title", []string{"Add feature"}},
		{"url", []string{"https://example.com/mr/1"}},
	}
	for _, tt := range tests {
		t.Run(tt.selector, func(t *testing.T) {
			if got := resolveSelector(view, tt.selector); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveSelector(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}

func TestResolveSelectorEmptyReserved(t *testing.T) {
	// An empty reserved field yields no values so it matches no pattern.
	view := gitlabView{Kind: "push"}
	if got := resolveSelector(view, "project"); got != nil {
		t.Errorf("resolveSelector(project) on empty = %v, want nil", got)
	}
	if got := resolveSelector(view, "branch"); got != nil {
		t.Errorf("resolveSelector(branch) with no ref = %v, want nil", got)
	}
}

func TestResolveSelectorRaw(t *testing.T) {
	view := gitlabView{
		Raw: map[string]any{
			"object_attributes": map[string]any{
				"iid":    float64(42),
				"draft":  true,
				"labels": []any{"bug", "urgent"},
				"note":   "hello",
			},
			"nested": map[string]any{"deep": "value"},
		},
	}
	tests := []struct {
		name     string
		selector string
		want     []string
	}{
		{"raw scalar string", "object_attributes.note", []string{"hello"}},
		{"raw scalar number", "object_attributes.iid", []string{"42"}},
		{"raw scalar bool", "object_attributes.draft", []string{"true"}},
		{"raw array of scalars", "object_attributes.labels", []string{"bug", "urgent"}},
		{"raw nested path", "nested.deep", []string{"value"}},
		{"missing path", "object_attributes.missing", nil},
		{"path through non-object", "object_attributes.note.x", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSelector(view, tt.selector)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveSelector(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}

func TestGitlabSelectorStringsSkipsObjects(t *testing.T) {
	// GitLab often encodes labels as objects; those elements are not scalars and
	// are skipped rather than rendered as a Go map string.
	labels := []any{
		map[string]any{"title": "bug"},
		"urgent",
	}
	got := gitlabSelectorStrings(labels)
	want := []string{"urgent"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gitlabSelectorStrings(objects) = %v, want %v", got, want)
	}
}

// mustPattern compiles a pattern for tests, failing fast on error.
func mustPattern(t *testing.T, pattern string) patternMatcher {
	t.Helper()
	m, err := compilePattern(pattern)
	if err != nil {
		t.Fatalf("compilePattern(%q): %v", pattern, err)
	}
	return m
}

func TestRouteMatches(t *testing.T) {
	view := gitlabView{
		Kind:     "merge_request",
		EventKey: "merge_request.open",
		Project:  "group/backend",
		Raw: map[string]any{
			"project":           map[string]any{"path_with_namespace": "group/backend"},
			"object_attributes": map[string]any{"target_branch": "main"},
		},
	}
	tests := []struct {
		name  string
		route compiledRoute
		want  bool
	}{
		{
			name:  "empty conds is catch-all",
			route: compiledRoute{},
			want:  true,
		},
		{
			name: "single condition hit",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "project", matchers: []patternMatcher{mustPattern(t, "group/backend")}},
			}},
			want: true,
		},
		{
			name: "single condition miss",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "project", matchers: []patternMatcher{mustPattern(t, "group/frontend")}},
			}},
			want: false,
		},
		{
			name: "all conds must hold (AND) - all hit",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "project", matchers: []patternMatcher{mustPattern(t, "group/*")}},
				{selector: "branch", matchers: []patternMatcher{mustPattern(t, "main")}},
			}},
			want: true,
		},
		{
			name: "all conds must hold (AND) - one miss",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "project", matchers: []patternMatcher{mustPattern(t, "group/*")}},
				{selector: "branch", matchers: []patternMatcher{mustPattern(t, "develop")}},
			}},
			want: false,
		},
		{
			name: "event selector matches through event matcher",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "event", event: eventMatcher{"merge_request.open"}},
			}},
			want: true,
		},
		{
			name: "event selector no match",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "event", event: eventMatcher{"pipeline.failed"}},
			}},
			want: false,
		},
		{
			name: "omitted selector places no constraint",
			route: compiledRoute{conds: []compiledCondition{
				{selector: "branch", matchers: []patternMatcher{mustPattern(t, "main")}},
			}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeMatches(view, tt.route); got != tt.want {
				t.Errorf("routeMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateRoutes(t *testing.T) {
	view := gitlabView{
		Kind:     "merge_request",
		EventKey: "merge_request.open",
		Project:  "group/backend",
		Raw: map[string]any{
			"project":           map[string]any{"path_with_namespace": "group/backend"},
			"object_attributes": map[string]any{"target_branch": "main"},
		},
	}

	condProject := func(pat string) compiledCondition {
		return compiledCondition{selector: "project", matchers: []patternMatcher{mustPattern(t, pat)}}
	}
	condBranch := func(pat string) compiledCondition {
		return compiledCondition{selector: "branch", matchers: []patternMatcher{mustPattern(t, pat)}}
	}

	tests := []struct {
		name        string
		routes      []compiledRoute
		wantChats   []string
		wantMatched bool
	}{
		{
			name:        "no routes matched",
			routes:      []compiledRoute{{conds: []compiledCondition{condProject("group/frontend")}, chats: []string{"c1"}}},
			wantChats:   nil,
			wantMatched: false,
		},
		{
			name:        "single match",
			routes:      []compiledRoute{{conds: []compiledCondition{condProject("group/backend")}, chats: []string{"c1"}}},
			wantChats:   []string{"c1"},
			wantMatched: true,
		},
		{
			name: "multiple matches union chats",
			routes: []compiledRoute{
				{conds: []compiledCondition{condProject("group/*")}, chats: []string{"c1"}},
				{conds: []compiledCondition{condBranch("main")}, chats: []string{"c2"}},
			},
			wantChats:   []string{"c1", "c2"},
			wantMatched: true,
		},
		{
			name: "dedup preserves first-seen order",
			routes: []compiledRoute{
				{conds: []compiledCondition{condProject("group/*")}, chats: []string{"c1", "c2"}},
				{conds: []compiledCondition{condBranch("main")}, chats: []string{"c2", "c3"}},
			},
			wantChats:   []string{"c1", "c2", "c3"},
			wantMatched: true,
		},
		{
			name: "stop halts scan after collecting its chats",
			routes: []compiledRoute{
				{conds: []compiledCondition{condProject("group/*")}, chats: []string{"c1"}, stop: true},
				{conds: []compiledCondition{condBranch("main")}, chats: []string{"c2"}},
			},
			wantChats:   []string{"c1"},
			wantMatched: true,
		},
		{
			name: "non-matching rule does not stop scan",
			routes: []compiledRoute{
				{conds: []compiledCondition{condProject("group/frontend")}, chats: []string{"c1"}, stop: true},
				{conds: []compiledCondition{condBranch("main")}, chats: []string{"c2"}},
			},
			wantChats:   []string{"c2"},
			wantMatched: true,
		},
		{
			name:        "catch-all matches",
			routes:      []compiledRoute{{chats: []string{"c1"}}},
			wantChats:   []string{"c1"},
			wantMatched: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChats, gotMatched := evaluateRoutes(tt.routes, view)
			if gotMatched != tt.wantMatched {
				t.Errorf("matched = %v, want %v", gotMatched, tt.wantMatched)
			}
			if !reflect.DeepEqual(gotChats, tt.wantChats) {
				t.Errorf("chats = %v, want %v", gotChats, tt.wantChats)
			}
		})
	}
}

func TestConditionMatches(t *testing.T) {
	globBackend, _ := compilePattern("group/backend/*")
	exactMain, _ := compilePattern("main")
	reRelease, _ := compilePattern(`/^release-/`)

	tests := []struct {
		name     string
		values   []string
		matchers []patternMatcher
		want     bool
	}{
		{"single value single matcher hit", []string{"group/backend/api"}, []patternMatcher{globBackend}, true},
		{"single value single matcher miss", []string{"group/frontend/api"}, []patternMatcher{globBackend}, false},
		{"any value matches (array label)", []string{"bug", "main"}, []patternMatcher{exactMain}, true},
		{"any matcher matches", []string{"release-1"}, []patternMatcher{exactMain, reRelease}, true},
		{"no values never matches", nil, []patternMatcher{exactMain}, false},
		{"no matchers never matches", []string{"main"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conditionMatches(tt.values, tt.matchers); got != tt.want {
				t.Errorf("conditionMatches(%v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}

func TestCompileGitlabRoutesEmpty(t *testing.T) {
	// No routes and empty routes both yield nil, preserving single-chat behaviour.
	for _, in := range [][]config.GitlabRouteYAMLConfig{nil, {}} {
		routes, err := CompileGitlabRoutes(in)
		if err != nil {
			t.Fatalf("CompileGitlabRoutes(%v) error: %v", in, err)
		}
		if routes != nil {
			t.Errorf("CompileGitlabRoutes(%v) = %v, want nil", in, routes)
		}
	}
}

func TestCompileGitlabRoutesGlobRegexEvent(t *testing.T) {
	in := []config.GitlabRouteYAMLConfig{
		{
			Match: map[string][]string{
				"project": {"group/backend/*"},
				"branch":  {`/^release-/`},
				"event":   {"merge_request.open", "push"},
			},
			Chats: []string{"backend"},
			Stop:  true,
		},
		{
			Match: map[string][]string{},
			Chats: []string{"catch-all"},
		},
	}

	routes, err := CompileGitlabRoutes(in)
	if err != nil {
		t.Fatalf("CompileGitlabRoutes error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("compiled %d routes, want 2", len(routes))
	}

	// First route: chats/stop preserved, conditions sorted deterministically
	// (branch, event, project), with the right matcher type per selector.
	r0 := routes[0]
	if !reflect.DeepEqual(r0.chats, []string{"backend"}) || !r0.stop {
		t.Fatalf("route0 chats=%v stop=%v, want [backend] true", r0.chats, r0.stop)
	}
	if len(r0.conds) != 3 {
		t.Fatalf("route0 has %d conds, want 3", len(r0.conds))
	}
	wantSelectors := []string{"branch", "event", "project"}
	for i, want := range wantSelectors {
		if r0.conds[i].selector != want {
			t.Errorf("route0 cond[%d].selector = %q, want %q", i, r0.conds[i].selector, want)
		}
	}
	// branch -> regex, event -> eventMatcher, project -> glob.
	if _, ok := r0.conds[0].matchers[0].(regexMatcher); !ok {
		t.Errorf("branch matcher = %T, want regexMatcher", r0.conds[0].matchers[0])
	}
	if !reflect.DeepEqual([]string(r0.conds[1].event), []string{"merge_request.open", "push"}) {
		t.Errorf("event matcher = %v, want [merge_request.open push]", r0.conds[1].event)
	}
	if r0.conds[1].matchers != nil {
		t.Errorf("event condition should carry no value matchers, got %v", r0.conds[1].matchers)
	}
	if _, ok := r0.conds[2].matchers[0].(globMatcher); !ok {
		t.Errorf("project matcher = %T, want globMatcher", r0.conds[2].matchers[0])
	}

	// Second route: empty match -> catch-all with no conditions.
	if len(routes[1].conds) != 0 {
		t.Errorf("route1 has %d conds, want 0 (catch-all)", len(routes[1].conds))
	}

	// End-to-end: the compiled rules route as expected.
	view := gitlabView{
		Kind:     "merge_request",
		EventKey: "merge_request.open",
		Project:  "group/backend/api",
		Raw: map[string]any{
			"project":           map[string]any{"path_with_namespace": "group/backend/api"},
			"object_attributes": map[string]any{"target_branch": "release-2"},
		},
	}
	chats, matched := evaluateRoutes(routes, view)
	if !matched {
		t.Fatal("expected a match")
	}
	// stop:true on route0 halts the scan before the catch-all contributes.
	if !reflect.DeepEqual(chats, []string{"backend"}) {
		t.Errorf("chats = %v, want [backend]", chats)
	}
}

func TestCompileGitlabRoutesBadRegex(t *testing.T) {
	in := []config.GitlabRouteYAMLConfig{
		{
			Match: map[string][]string{"branch": {`/[unterminated/`}},
			Chats: []string{"c"},
		},
	}
	if _, err := CompileGitlabRoutes(in); err == nil {
		t.Fatal("expected error for malformed regex, got nil")
	}
}
