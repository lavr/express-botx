package cmd

import (
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

// serve and serve --enqueue never call Config.Validate, so an explicitly empty
// scope has to be rejected where keys are resolved. Falling back to "no scope"
// there would silently hand the key full access.
func TestResolveAPIKeys_EmptyScopeRejected(t *testing.T) {
	chats := map[string]config.ChatConfig{"known": {ID: "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"}}

	if _, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", Chats: []string{}}}, chats); err == nil {
		t.Error("empty chats list accepted; the key would be unrestricted")
	}

	keys, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v"}}, chats)
	if err != nil {
		t.Fatalf("absent scope rejected: %v", err)
	}
	if len(keys[0].Chats) != 0 {
		t.Errorf("absent scope produced %v, want unrestricted", keys[0].Chats)
	}
}

func TestResolveAPIKeys_AliasResolvedToUUID(t *testing.T) {
	chats := map[string]config.ChatConfig{"known": {ID: "BCB715A2-E8D3-57A8-AB3B-6A14C044DD22"}}
	keys, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", Chats: []string{"known"}}}, chats)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := keys[0].Chats[0]; got != "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22" {
		t.Errorf("scope = %q, want the lowercased UUID behind the alias", got)
	}
}

func TestResolveAPIKeys_DefaultChat(t *testing.T) {
	chats := map[string]config.ChatConfig{
		"known": {ID: "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22", Bot: "bot-a"},
		"other": {ID: "7ee8aaa9-c6cb-5ee6-8445-7d654819b285"},
	}
	tests := []struct {
		name  string
		chats []string
		want  string
	}{
		{"unscoped key has none", nil, ""},
		{"sole alias kept as written", []string{"known"}, "known"},
		{"sole uuid kept as written", []string{"7ee8aaa9-c6cb-5ee6-8445-7d654819b285"}, "7ee8aaa9-c6cb-5ee6-8445-7d654819b285"},
		{"several chats have none", []string{"known", "other"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", Chats: tt.chats}}, chats)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got := keys[0].DefaultChat; got != tt.want {
				t.Errorf("DefaultChat = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveAPIKeys_Duplicates(t *testing.T) {
	chats := map[string]config.ChatConfig{}

	// A repeated name is only confusing — the scope travels with the credential,
	// not with the name — so it warns and starts rather than breaking a
	// deployment that has been running with it.
	keys, err := resolveAPIKeys([]config.APIKeyConfig{
		{Name: "same", Key: "a"},
		{Name: "same", Key: "b"},
	}, chats)
	if err != nil {
		t.Errorf("duplicate names refused startup: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("got %d keys, want both kept", len(keys))
	}

}

// Collision detection has to run over the assembled key set, not over the
// configured keys alone: --api-key and EXPRESS_BOTX_SERVER_API_KEY are appended
// afterwards without a scope, and the server indexes keys by value, so a
// collision would silently replace a scoped entry with an unrestricted one.
func TestCheckKeyCollisions(t *testing.T) {
	scoped := server.ResolvedKey{Name: "b2c-at", Key: "shared", Chats: []string{"bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"}}

	if err := checkKeyCollisions([]server.ResolvedKey{scoped, {Name: "env", Key: "shared"}}); err == nil {
		t.Error("env key silently took over a scoped key's value")
	}
	if err := checkKeyCollisions([]server.ResolvedKey{scoped, {Name: "cli", Key: "shared"}}); err == nil {
		t.Error("cli key silently took over a scoped key's value")
	}
	if err := checkKeyCollisions([]server.ResolvedKey{scoped, {Name: "env", Key: "distinct"}}); err != nil {
		t.Errorf("distinct values rejected: %v", err)
	}
}

// A reference must mean the same chat when a scope is built and when a message
// is addressed. Delivery resolves a UUID before consulting the alias map, so a
// scope that consulted aliases first would bind to a different chat whenever an
// alias is itself named like a UUID.
func TestResolveKeyScope_UUIDBeatsAliasOfTheSameName(t *testing.T) {
	const (
		a = "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"
		b = "7ee8aaa9-c6cb-5ee6-8445-7d654819b285"
	)
	chats := map[string]config.ChatConfig{a: {ID: b}}

	keys, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", Chats: []string{a}}}, chats)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := keys[0].Chats[0]; got != a {
		t.Errorf("scope = %q, want %q: the reference is a UUID and delivery reads it as one", got, a)
	}
}
