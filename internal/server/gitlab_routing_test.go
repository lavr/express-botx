package server

import (
	"reflect"
	"testing"
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
