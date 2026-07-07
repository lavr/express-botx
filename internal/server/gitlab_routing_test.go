package server

import "testing"

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
