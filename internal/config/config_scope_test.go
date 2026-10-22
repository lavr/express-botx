package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidate_APIKeyChatScope(t *testing.T) {
	base := func(chats []string) *Config {
		return &Config{
			Bots:  map[string]BotConfig{"b": {Host: "h", ID: "i", Secret: "s"}},
			Chats: map[string]ChatConfig{"known": {ID: "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"}},
			Server: ServerConfig{
				APIKeys: []APIKeyConfig{{Name: "k", Key: "v", Chats: chats}},
			},
		}
	}

	tests := []struct {
		name    string
		chats   []string
		wantErr bool
	}{
		{"absent scope", nil, false},
		{"known alias", []string{"known"}, false},
		{"bare uuid", []string{"7ee8aaa9-c6cb-5ee6-8445-7d654819b285"}, false},
		{"empty list", []string{}, true},
		{"unknown alias", []string{"nope"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hasErr bool
			for _, r := range base(tc.chats).Validate(nil) {
				if r.Level == ValidationError && strings.Contains(r.Path, "api_keys") {
					hasErr = true
				}
			}
			if hasErr != tc.wantErr {
				t.Errorf("error = %v, want %v", hasErr, tc.wantErr)
			}
		})
	}
}

func TestValidate_ChatsWhereMember(t *testing.T) {
	validate := func(k APIKeyConfig, mc *MembershipCacheConfig) []ValidationResult {
		cfg := &Config{
			Bots:  map[string]BotConfig{"b": {Host: "h", ID: "2a8e7c41-5b0d-4f3e-9c6a-1d2e3f405162", Secret: "s"}},
			Chats: map[string]ChatConfig{"known": {ID: "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"}},
			Server: ServerConfig{
				APIKeys:         []APIKeyConfig{k},
				MembershipCache: mc,
			},
		}
		var errs []ValidationResult
		for _, r := range cfg.Validate(nil) {
			if r.Level == ValidationError {
				errs = append(errs, r)
			}
		}
		return errs
	}

	if errs := validate(APIKeyConfig{Name: "k", Key: "v", ChatsWhereMember: true}, nil); len(errs) != 0 {
		t.Errorf("membership key alone: %v", errs)
	}
	errs := validate(APIKeyConfig{Name: "k", Key: "v", ChatsWhereMember: true, Chats: []string{"known"}}, nil)
	if len(errs) != 1 || errs[0].Path != "server.api_keys[0].chats_where_member" {
		t.Errorf("membership with chats: %v, want one error on chats_where_member", errs)
	}

	for _, mc := range []*MembershipCacheConfig{
		{TTL: "soon"},
		{TTL: "0s"},
		{NegativeTTL: "-5s"},
		{TTL: "10s", NegativeTTL: "30s"},
		{NegativeTTL: "2m"}, // longer than the default ttl
	} {
		if errs := validate(APIKeyConfig{Name: "k", Key: "v", ChatsWhereMember: true}, mc); len(errs) != 1 || errs[0].Path != "server.membership_cache" {
			t.Errorf("membership_cache %+v: %v, want one error", *mc, errs)
		}
	}
	if errs := validate(APIKeyConfig{Name: "k", Key: "v"}, &MembershipCacheConfig{TTL: "2m", NegativeTTL: "20s"}); len(errs) != 0 {
		t.Errorf("valid membership_cache: %v", errs)
	}
}

func TestMembershipCacheDurations(t *testing.T) {
	ttl, neg, err := (*MembershipCacheConfig)(nil).Durations()
	if err != nil || ttl != DefaultMembershipTTL || neg != DefaultMembershipNegativeTTL {
		t.Errorf("defaults: %v %v %v", ttl, neg, err)
	}
	ttl, neg, err = (&MembershipCacheConfig{TTL: "90s"}).Durations()
	if err != nil || ttl.Seconds() != 90 || neg != DefaultMembershipNegativeTTL {
		t.Errorf("ttl only: %v %v %v", ttl, neg, err)
	}
}

// The new keys are known to config validate, so they draw no warnings.
func TestValidate_ChatsWhereMemberKeysAreKnown(t *testing.T) {
	raw := []byte(`
bots:
  b: {host: h, id: 2a8e7c41-5b0d-4f3e-9c6a-1d2e3f405162, secret: s}
server:
  membership_cache:
    ttl: 60s
    negative_ttl: 15s
  api_keys:
    - name: controller
      key: env:KEY
      chats_where_member: true
`)
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Validate(raw) {
		if strings.Contains(r.Message, "unknown key") {
			t.Errorf("%s: %s", r.Path, r.Message)
		}
	}
}
