package mentions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParse_EmptyMessage(t *testing.T) {
	r := Parse(context.Background(), "", nil, true, nil)
	if r.Message != "" {
		t.Errorf("expected empty message, got %q", r.Message)
	}
	if r.Mentions != nil {
		t.Errorf("expected nil mentions, got %s", r.Mentions)
	}
	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestParse_NoTokens(t *testing.T) {
	msg := "Hello, this is a regular message without any mentions."
	r := Parse(context.Background(), msg, nil, true, nil)
	if r.Message != msg {
		t.Errorf("expected message unchanged, got %q", r.Message)
	}
	if r.Mentions != nil {
		t.Errorf("expected nil mentions, got %s", r.Mentions)
	}
	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestParse_Disabled(t *testing.T) {
	msg := "Hello @mention[email:test@example.com]"
	r := Parse(context.Background(), msg, nil, false, nil)
	if r.Message != msg {
		t.Errorf("expected message unchanged when parse disabled, got %q", r.Message)
	}
}

func TestScan_NoTokens(t *testing.T) {
	spans := scan("Hello world, no mentions here.")
	if len(spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(spans))
	}
}

func TestScan_SingleToken(t *testing.T) {
	msg := "Hello @mention[email:test@example.com] world"
	spans := scan(msg)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got := spans[0].raw(msg)
	want := "@mention[email:test@example.com]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestScan_MultipleTokens(t *testing.T) {
	msg := "Hi @mention[all] and @mention[huid:abc-123] bye"
	spans := scan(msg)
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if got := spans[0].raw(msg); got != "@mention[all]" {
		t.Errorf("span 0: got %q, want %q", got, "@mention[all]")
	}
	if got := spans[1].raw(msg); got != "@mention[huid:abc-123]" {
		t.Errorf("span 1: got %q, want %q", got, "@mention[huid:abc-123]")
	}
}

func TestScan_UnclosedToken(t *testing.T) {
	msg := "Hello @mention[email:test@example.com no closing bracket"
	spans := scan(msg)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span for unclosed token, got %d", len(spans))
	}
	if !spans[0].unclosed {
		t.Error("expected span to be marked unclosed")
	}
}

func TestScan_NewlineInsideToken(t *testing.T) {
	msg := "Hello @mention[email:\ntest@example.com] world"
	spans := scan(msg)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span for newline-broken token, got %d", len(spans))
	}
	if !spans[0].unclosed {
		t.Error("expected span to be marked unclosed")
	}
}

// --- Step 2: grammar parser tests ---

func TestParseTokenBody_EmailNoDisplayName(t *testing.T) {
	pt, err := parseTokenBody("email:user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.resolver != "email" {
		t.Errorf("resolver = %q, want %q", pt.resolver, "email")
	}
	if pt.value != "user@example.com" {
		t.Errorf("value = %q, want %q", pt.value, "user@example.com")
	}
	if pt.displayName != "" {
		t.Errorf("displayName = %q, want empty", pt.displayName)
	}
}

func TestParseTokenBody_EmailWithURLQuotedDisplayName(t *testing.T) {
	pt, err := parseTokenBody("email:user@example.com;John%20Doe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.resolver != "email" {
		t.Errorf("resolver = %q, want %q", pt.resolver, "email")
	}
	if pt.value != "user@example.com" {
		t.Errorf("value = %q, want %q", pt.value, "user@example.com")
	}
	if pt.displayName != "John Doe" {
		t.Errorf("displayName = %q, want %q", pt.displayName, "John Doe")
	}
}

func TestParseTokenBody_HuidNoDisplayName(t *testing.T) {
	pt, err := parseTokenBody("huid:550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.resolver != "huid" {
		t.Errorf("resolver = %q, want %q", pt.resolver, "huid")
	}
	if pt.value != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("value = %q, want %q", pt.value, "550e8400-e29b-41d4-a716-446655440000")
	}
	if pt.displayName != "" {
		t.Errorf("displayName = %q, want empty", pt.displayName)
	}
}

func TestParseTokenBody_HuidWithDisplayName(t *testing.T) {
	pt, err := parseTokenBody("huid:550e8400-e29b-41d4-a716-446655440000;Alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.resolver != "huid" {
		t.Errorf("resolver = %q, want %q", pt.resolver, "huid")
	}
	if pt.value != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("value = %q, want %q", pt.value, "550e8400-e29b-41d4-a716-446655440000")
	}
	if pt.displayName != "Alice" {
		t.Errorf("displayName = %q, want %q", pt.displayName, "Alice")
	}
}

func TestParseTokenBody_All(t *testing.T) {
	pt, err := parseTokenBody("all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.resolver != "all" {
		t.Errorf("resolver = %q, want %q", pt.resolver, "all")
	}
	if pt.value != "" {
		t.Errorf("value = %q, want empty", pt.value)
	}
	if pt.displayName != "" {
		t.Errorf("displayName = %q, want empty", pt.displayName)
	}
}

func TestParseTokenBody_EmailEmptyValue(t *testing.T) {
	_, err := parseTokenBody("email:")
	if err == nil {
		t.Fatal("expected error for empty email value")
	}
}

func TestParseTokenBody_AllWithExtra(t *testing.T) {
	_, err := parseTokenBody("all;x")
	if err == nil {
		t.Fatal("expected error for @mention[all;x]")
	}
}

func TestParse_UnclosedToken(t *testing.T) {
	msg := "Hello @mention[email:test@example.com world"
	r := Parse(context.Background(), msg, nil, true, nil)
	if r.Message != msg {
		t.Errorf("expected message unchanged, got %q", r.Message)
	}
	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}
	if r.Errors[0].Kind != "parse" {
		t.Errorf("error kind = %q, want %q", r.Errors[0].Kind, "parse")
	}
	if r.Errors[0].Cause != "unclosed token" {
		t.Errorf("error cause = %q, want %q", r.Errors[0].Cause, "unclosed token")
	}
}

// --- Step 3: email normalization tests ---

// mockResolver implements UserResolver for tests.
type mockResolver struct {
	users map[string]struct{ huid, name string }
}

func (m *mockResolver) GetUserByEmail(_ context.Context, email string) (string, string, error) {
	u, ok := m.users[email]
	if !ok {
		return "", "", fmt.Errorf("user not found: %s", email)
	}
	return u.huid, u.name, nil
}

func setupTestIDGen(t *testing.T) func() string {
	t.Helper()
	counter := 0
	orig := newMentionID
	newMentionID = func() string {
		counter++
		return fmt.Sprintf("test-id-%d", counter)
	}
	t.Cleanup(func() { newMentionID = orig })
	return newMentionID
}

func TestNormalize_EmailLookupSuccess_NoDisplayName(t *testing.T) {
	setupTestIDGen(t)
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{
		"user@example.com": {huid: "aaa-bbb-ccc", name: "John Doe"},
	}}

	msg := "Hello @mention[email:user@example.com] world"
	r := Parse(context.Background(), msg, nil, true, resolver)

	// Message should have placeholder.
	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	// Mentions array should have one entry.
	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}

	m := mentions[0]
	if m["mention_id"] != "test-id-1" {
		t.Errorf("mention_id = %v, want %q", m["mention_id"], "test-id-1")
	}
	if m["mention_type"] != "user" {
		t.Errorf("mention_type = %v, want %q", m["mention_type"], "user")
	}
	data := m["mention_data"].(map[string]interface{})
	if data["user_huid"] != "aaa-bbb-ccc" {
		t.Errorf("user_huid = %v, want %q", data["user_huid"], "aaa-bbb-ccc")
	}
	// Without display name, should use name from lookup.
	if data["name"] != "John Doe" {
		t.Errorf("name = %v, want %q", data["name"], "John Doe")
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestNormalize_EmailLookupSuccess_WithDisplayName(t *testing.T) {
	setupTestIDGen(t)
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{
		"user@example.com": {huid: "aaa-bbb-ccc", name: "John Doe"},
	}}

	msg := "Hello @mention[email:user@example.com;Custom%20Name] world"
	r := Parse(context.Background(), msg, nil, true, resolver)

	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	data := mentions[0]["mention_data"].(map[string]interface{})
	// Display name override should take precedence.
	if data["name"] != "Custom Name" {
		t.Errorf("name = %v, want %q", data["name"], "Custom Name")
	}
}

func TestNormalize_EmailLookupFailure_TokenStaysLiteral(t *testing.T) {
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{}}

	msg := "Hello @mention[email:unknown@example.com] world"
	r := Parse(context.Background(), msg, nil, true, resolver)

	// Message should be unchanged - token stays as literal text.
	if r.Message != msg {
		t.Errorf("Message = %q, want unchanged %q", r.Message, msg)
	}

	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}
	if r.Errors[0].Kind != "lookup" {
		t.Errorf("error kind = %q, want %q", r.Errors[0].Kind, "lookup")
	}
	if r.Errors[0].Resolver != "email" {
		t.Errorf("error resolver = %q, want %q", r.Errors[0].Resolver, "email")
	}
	if !strings.Contains(r.Errors[0].Cause, "user not found") {
		t.Errorf("error cause = %q, want it to contain %q", r.Errors[0].Cause, "user not found")
	}
}

// --- Step 4: huid normalization tests ---

func TestNormalize_HuidNoDisplayName(t *testing.T) {
	setupTestIDGen(t)

	msg := "Hello @mention[huid:550e8400-e29b-41d4-a716-446655440000] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}

	m := mentions[0]
	if m["mention_id"] != "test-id-1" {
		t.Errorf("mention_id = %v, want %q", m["mention_id"], "test-id-1")
	}
	if m["mention_type"] != "user" {
		t.Errorf("mention_type = %v, want %q", m["mention_type"], "user")
	}
	data := m["mention_data"].(map[string]interface{})
	if data["user_huid"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("user_huid = %v, want %q", data["user_huid"], "550e8400-e29b-41d4-a716-446655440000")
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestNormalize_HuidNoDisplayName_NameAbsentInPayload(t *testing.T) {
	setupTestIDGen(t)

	msg := "Hello @mention[huid:550e8400-e29b-41d4-a716-446655440000] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	data := mentions[0]["mention_data"].(map[string]interface{})
	// Without display name, "name" field must be absent from the payload.
	if _, ok := data["name"]; ok {
		t.Errorf("expected 'name' field to be absent in mention_data, but it is present: %v", data["name"])
	}
}

func TestNormalize_HuidWithDisplayName(t *testing.T) {
	setupTestIDGen(t)

	msg := "Hello @mention[huid:550e8400-e29b-41d4-a716-446655440000;Alice%20B] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}

	data := mentions[0]["mention_data"].(map[string]interface{})
	if data["user_huid"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("user_huid = %v, want %q", data["user_huid"], "550e8400-e29b-41d4-a716-446655440000")
	}
	if data["name"] != "Alice B" {
		t.Errorf("name = %v, want %q", data["name"], "Alice B")
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestNormalize_EmailLookupFailure_NoMention(t *testing.T) {
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{}}

	msg := "Hello @mention[email:unknown@example.com] world"
	r := Parse(context.Background(), msg, nil, true, resolver)

	// Mentions should be nil - no mention generated on lookup failure.
	if r.Mentions != nil {
		t.Errorf("expected nil mentions on lookup failure, got %s", r.Mentions)
	}
}

// --- Step 5: all normalization tests ---

func TestNormalize_All(t *testing.T) {
	setupTestIDGen(t)

	msg := "Hello @mention[all] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}

	m := mentions[0]
	if m["mention_id"] != "test-id-1" {
		t.Errorf("mention_id = %v, want %q", m["mention_id"], "test-id-1")
	}
	if m["mention_type"] != "all" {
		t.Errorf("mention_type = %v, want %q", m["mention_type"], "all")
	}
	// mention_data must be null for "all" mentions.
	if m["mention_data"] != nil {
		t.Errorf("mention_data = %v, want nil", m["mention_data"])
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

// --- Step 6: merge with raw mentions tests ---

func TestMerge_RawAndParsedMention(t *testing.T) {
	setupTestIDGen(t)
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{
		"user@example.com": {huid: "aaa-bbb-ccc", name: "John Doe"},
	}}

	rawMention := json.RawMessage(`[{"mention_id":"raw-id-1","mention_type":"user","mention_data":{"user_huid":"ddd-eee-fff","name":"Raw User"}}]`)
	msg := "Hello @mention[email:user@example.com] world"
	r := Parse(context.Background(), msg, rawMention, true, resolver)

	want := "Hello @{mention:test-id-1} world"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions (1 raw + 1 parsed), got %d", len(mentions))
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

func TestMerge_RawMentionsUnchanged(t *testing.T) {
	setupTestIDGen(t)

	rawMention := json.RawMessage(`[{"mention_id":"raw-id-1","mention_type":"user","mention_data":{"user_huid":"ddd-eee-fff","name":"Raw User"}}]`)
	msg := "Hello @mention[huid:550e8400-e29b-41d4-a716-446655440000;Alice] world"
	r := Parse(context.Background(), msg, rawMention, true, nil)

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(mentions))
	}

	// First entry must be the original raw mention, unchanged.
	raw := mentions[0]
	if raw["mention_id"] != "raw-id-1" {
		t.Errorf("raw mention_id = %v, want %q", raw["mention_id"], "raw-id-1")
	}
	if raw["mention_type"] != "user" {
		t.Errorf("raw mention_type = %v, want %q", raw["mention_type"], "user")
	}
	data := raw["mention_data"].(map[string]interface{})
	if data["user_huid"] != "ddd-eee-fff" {
		t.Errorf("raw user_huid = %v, want %q", data["user_huid"], "ddd-eee-fff")
	}
	if data["name"] != "Raw User" {
		t.Errorf("raw name = %v, want %q", data["name"], "Raw User")
	}
}

func TestMerge_ParsedMentionsAppendedAtEnd(t *testing.T) {
	setupTestIDGen(t)
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{
		"user@example.com": {huid: "aaa-bbb-ccc", name: "John Doe"},
	}}

	rawMention := json.RawMessage(`[{"mention_id":"raw-id-1","mention_type":"user","mention_data":{"user_huid":"ddd-eee-fff","name":"Raw User"}}]`)
	msg := "Hello @mention[email:user@example.com] and @mention[all] world"
	r := Parse(context.Background(), msg, rawMention, true, resolver)

	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 3 {
		t.Fatalf("expected 3 mentions (1 raw + 2 parsed), got %d", len(mentions))
	}

	// Index 0: raw mention.
	if mentions[0]["mention_id"] != "raw-id-1" {
		t.Errorf("mentions[0] mention_id = %v, want %q", mentions[0]["mention_id"], "raw-id-1")
	}

	// Index 1: first parsed mention (email).
	if mentions[1]["mention_id"] != "test-id-1" {
		t.Errorf("mentions[1] mention_id = %v, want %q", mentions[1]["mention_id"], "test-id-1")
	}
	if mentions[1]["mention_type"] != "user" {
		t.Errorf("mentions[1] mention_type = %v, want %q", mentions[1]["mention_type"], "user")
	}

	// Index 2: second parsed mention (all).
	if mentions[2]["mention_id"] != "test-id-2" {
		t.Errorf("mentions[2] mention_id = %v, want %q", mentions[2]["mention_id"], "test-id-2")
	}
	if mentions[2]["mention_type"] != "all" {
		t.Errorf("mentions[2] mention_type = %v, want %q", mentions[2]["mention_type"], "all")
	}

	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got %v", r.Errors)
	}
}

// --- Step 7: parser limit tests ---

func TestParse_LimitExceeded(t *testing.T) {
	setupTestIDGen(t)

	// Temporarily lower the limit to make the test feasible.
	origLimit := MaxParsedMentions
	defer func() { maxParsedMentions = origLimit }()
	maxParsedMentions = 3

	msg := "@mention[huid:aaa] @mention[huid:bbb] @mention[huid:ccc] @mention[huid:ddd] @mention[huid:eee]"
	r := Parse(context.Background(), msg, nil, true, nil)

	// First 3 tokens should be normalized, last 2 should stay literal.
	if !strings.Contains(r.Message, "@{mention:test-id-1}") {
		t.Errorf("expected first token normalized, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "@{mention:test-id-2}") {
		t.Errorf("expected second token normalized, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "@{mention:test-id-3}") {
		t.Errorf("expected third token normalized, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "@mention[huid:ddd]") {
		t.Errorf("expected fourth token as literal text, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "@mention[huid:eee]") {
		t.Errorf("expected fifth token as literal text, got %q", r.Message)
	}

	// Should have exactly 3 mentions.
	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 3 {
		t.Errorf("expected 3 mentions, got %d", len(mentions))
	}

	// Should have a limit error.
	hasLimitErr := false
	for _, e := range r.Errors {
		if e.Kind == "limit" {
			hasLimitErr = true
			break
		}
	}
	if !hasLimitErr {
		t.Errorf("expected a limit error, got errors: %v", r.Errors)
	}
}

func TestParse_LimitExceeded_MessageStillReturned(t *testing.T) {
	setupTestIDGen(t)

	origLimit := MaxParsedMentions
	defer func() { maxParsedMentions = origLimit }()
	maxParsedMentions = 1

	msg := "Hello @mention[huid:aaa] and @mention[huid:bbb] bye"
	r := Parse(context.Background(), msg, nil, true, nil)

	// Message must be returned (not empty, not error).
	if r.Message == "" {
		t.Error("expected non-empty message")
	}

	// First token normalized, second stays literal.
	want := "Hello @{mention:test-id-1} and @mention[huid:bbb] bye"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}

	// Mentions should still be present for the successfully parsed token.
	var mentions []map[string]interface{}
	if err := json.Unmarshal(r.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Errorf("expected 1 mention, got %d", len(mentions))
	}

	// Errors should include the limit error but message was still sent.
	if len(r.Errors) == 0 {
		t.Error("expected at least one error (limit)")
	}
}

// --- Step 8: parser error record tests ---

func TestParseError_ParseRecord(t *testing.T) {
	msg := "Hello @mention[email:] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}

	e := r.Errors[0]
	if e.Kind != "parse" {
		t.Errorf("Kind = %q, want %q", e.Kind, "parse")
	}
	if e.Token != "@mention[email:]" {
		t.Errorf("Token = %q, want %q", e.Token, "@mention[email:]")
	}
	if e.Resolver != "" {
		t.Errorf("Resolver = %q, want empty (not determined for parse errors)", e.Resolver)
	}
	if e.Value != "" {
		t.Errorf("Value = %q, want empty (not determined for parse errors)", e.Value)
	}
	if e.Cause == "" {
		t.Error("Cause must not be empty")
	}
}

func TestParseError_LookupRecord(t *testing.T) {
	resolver := &mockResolver{users: map[string]struct{ huid, name string }{}}

	msg := "Hello @mention[email:unknown@example.com] world"
	r := Parse(context.Background(), msg, nil, true, resolver)

	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}

	e := r.Errors[0]
	if e.Kind != "lookup" {
		t.Errorf("Kind = %q, want %q", e.Kind, "lookup")
	}
	if e.Token != "@mention[email:unknown@example.com]" {
		t.Errorf("Token = %q, want %q", e.Token, "@mention[email:unknown@example.com]")
	}
	if e.Resolver != "email" {
		t.Errorf("Resolver = %q, want %q", e.Resolver, "email")
	}
	if e.Value != "unknown@example.com" {
		t.Errorf("Value = %q, want %q", e.Value, "unknown@example.com")
	}
	if e.Cause == "" {
		t.Error("Cause must not be empty")
	}
	if !strings.Contains(e.Cause, "user not found") {
		t.Errorf("Cause = %q, want it to contain %q", e.Cause, "user not found")
	}
}

func TestParseError_LimitRecord(t *testing.T) {
	setupTestIDGen(t)

	origLimit := MaxParsedMentions
	defer func() { maxParsedMentions = origLimit }()
	maxParsedMentions = 1

	msg := "@mention[huid:aaa] @mention[huid:bbb]"
	r := Parse(context.Background(), msg, nil, true, nil)

	// Find the limit error among accumulated errors.
	var limitErr *ParseError
	for i := range r.Errors {
		if r.Errors[i].Kind == "limit" {
			limitErr = &r.Errors[i]
			break
		}
	}
	if limitErr == nil {
		t.Fatalf("expected a limit error, got errors: %v", r.Errors)
	}

	if limitErr.Token != "" {
		t.Errorf("Token = %q, want empty (limit errors are not token-specific)", limitErr.Token)
	}
	if limitErr.Resolver != "" {
		t.Errorf("Resolver = %q, want empty", limitErr.Resolver)
	}
	if limitErr.Value != "" {
		t.Errorf("Value = %q, want empty", limitErr.Value)
	}
	if limitErr.Cause == "" {
		t.Error("Cause must not be empty")
	}
	if !strings.Contains(limitErr.Cause, fmt.Sprintf("%d", maxParsedMentions)) {
		t.Errorf("Cause = %q, want it to mention the limit %d", limitErr.Cause, maxParsedMentions)
	}
}

func TestNormalize_AllWithExtra_ParseError(t *testing.T) {
	msg := "Hello @mention[all;everyone] world"
	r := Parse(context.Background(), msg, nil, true, nil)

	// Token should stay as literal text.
	if r.Message != msg {
		t.Errorf("Message = %q, want unchanged %q", r.Message, msg)
	}

	// No mentions should be generated.
	if r.Mentions != nil {
		t.Errorf("expected nil mentions, got %s", r.Mentions)
	}

	// Should have a parse error.
	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}
	if r.Errors[0].Kind != "parse" {
		t.Errorf("error kind = %q, want %q", r.Errors[0].Kind, "parse")
	}
}
