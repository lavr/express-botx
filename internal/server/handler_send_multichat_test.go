package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/mentions"
)

// Task 4: /send sync fans out over a comma-separated chat_id. Each target
// resolves its own chat and bot, and mentions are parsed under that bot's own
// resolver — a chat-bound bot can differ per chat in multi-bot setups. The
// response is always the uniform MultiSendResponse, even for a single chat.

// fakeUserResolver implements mentions.UserResolver, returning a fixed huid/name
// for every email so a test can tell which per-bot resolver ran by the huid that
// ends up in the resulting mention.
type fakeUserResolver struct {
	huid string
	name string
}

func (f fakeUserResolver) GetUserByEmail(_ context.Context, _ string) (string, string, error) {
	return f.huid, f.name, nil
}

// newMultiBotSendServer builds a two-bot server whose chat resolver binds chatA
// -> botA and chatB -> botB, with a distinct per-bot mentions resolver each, so
// per-target bot + mention resolution can be observed. failChats resolve to an
// error (for partial/all-fail cases).
func newMultiBotSendServer(sendFn SendFunc, failChats ...string) *Server {
	fail := make(map[string]bool, len(failChats))
	for _, c := range failChats {
		fail[c] = true
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if fail[chatID] {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		bound := ""
		switch chatID {
		case "chatA":
			bound = "botA"
		case "chatB":
			bound = "botB"
		}
		return ChatResolveResult{ChatID: chatID, Bot: bound}, nil
	}
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
		BotNames: []string{"botA", "botB"},
	}
	return New(cfg, sendFn, chatResolver,
		WithBotMentionsResolvers(map[string]mentions.UserResolver{
			"botA": fakeUserResolver{huid: "huid-A", name: "Alice A"},
			"botB": fakeUserResolver{huid: "huid-B", name: "Bob B"},
		}),
	)
}

func TestSendSync_SingleChat_UnifiedResponse(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	srv := newMultiBotSendServer(send)
	body := `{"chat_id":"chatA","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 1 result, 0 errors", resp)
	}
	if resp.Results[0].Chat != "chatA" || resp.Results[0].SyncID != "sync-1" {
		t.Errorf("results[0] = %+v, want chatA -> sync-1", resp.Results[0])
	}
	if cap.count() != 1 || cap.last().Bot != "botA" {
		t.Errorf("send: count=%d bot=%q, want 1 send to botA", cap.count(), cap.last().Bot)
	}
}

func TestSendSync_MultiChat_PerBotMentions(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-" + p.ChatID, nil
	}
	srv := newMultiBotSendServer(send)
	// Same message with an inline email mention: chatA -> botA's resolver
	// (huid-A), chatB -> botB's resolver (huid-B). If mentions were parsed once
	// up front with a single resolver, both chats would carry the same huid.
	body := `{"chat_id":"chatA,chatB","message":"hi @mention[email:x@example.com]"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 2 results", resp)
	}
	if resp.Results[0].Chat != "chatA" || resp.Results[1].Chat != "chatB" {
		t.Fatalf("results = %+v, want chatA then chatB", resp.Results)
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2", cap.count())
	}
	// Verify each chat was sent under its own bot with its own resolver's huid.
	byChat := map[string]*SendPayload{}
	for _, c := range cap.calls {
		byChat[c.ChatID] = c
	}
	a, b := byChat["chatA"], byChat["chatB"]
	if a == nil || b == nil {
		t.Fatalf("missing captured payloads: %+v", byChat)
	}
	if a.Bot != "botA" || b.Bot != "botB" {
		t.Errorf("bots = %q/%q, want botA/botB", a.Bot, b.Bot)
	}
	if !strings.Contains(string(a.Mentions), "huid-A") {
		t.Errorf("chatA mentions = %s, want huid-A (botA resolver)", a.Mentions)
	}
	if !strings.Contains(string(b.Mentions), "huid-B") {
		t.Errorf("chatB mentions = %s, want huid-B (botB resolver)", b.Mentions)
	}
}

func TestSendSync_MultiChat_PartialFailure(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	srv := newMultiBotSendServer(send, "chatB")
	body := `{"chat_id":"chatA,chatB","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (partial), body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 1 {
		t.Fatalf("response = %+v, want ok with 1 result, 1 error", resp)
	}
	if resp.Results[0].Chat != "chatA" {
		t.Errorf("results[0].Chat = %q, want chatA", resp.Results[0].Chat)
	}
	if resp.Errors[0].Chat != "chatB" {
		t.Errorf("errors[0].Chat = %q, want chatB", resp.Errors[0].Chat)
	}
	if cap.count() != 1 {
		t.Errorf("send count = %d, want 1 (only chatA)", cap.count())
	}
}

func TestSendSync_MultiChat_AllFail(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	srv := newMultiBotSendServer(send, "chatA", "chatB")
	body := `{"chat_id":"chatA,chatB","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (all failed), body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
		t.Fatalf("response = %+v, want not-ok with 0 results, 2 errors", resp)
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0 (nothing resolved)", cap.count())
	}
}

func TestSendSync_MultiChat_Dedup(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	srv := newMultiBotSendServer(send)
	body := `{"chat_id":"chatA, chatA ,chatB,chatA","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if len(resp.Results) != 2 || resp.Results[0].Chat != "chatA" || resp.Results[1].Chat != "chatB" {
		t.Fatalf("results = %+v, want deduped chatA then chatB", resp.Results)
	}
	if cap.count() != 2 {
		t.Errorf("send count = %d, want 2 (deduped)", cap.count())
	}
}

// Task 5: async /send expands a comma-separated chat_id into N independent
// enqueues — one queued message per chat — and returns the uniform
// MultiSendResponse with 202, one SendResult{Chat, RequestID, Queued} per chat.

// newAsyncSendServer builds an async-mode server whose enqueue stub records every
// payload and returns a per-chat request id. Uses mixed routing so chat aliases
// with a bot alias pass request-level validation.
func newAsyncSendServer(sendFn SendFunc) *Server {
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "mixed",
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	return New(cfg, sendFn, chatResolver)
}

func TestSendAsync_SingleChat_UnifiedResponse(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "req-" + p.ChatID, nil
	}
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":"deploy","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 202 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 1 result", resp)
	}
	r0 := resp.Results[0]
	if r0.Chat != "deploy" || r0.RequestID != "req-deploy" || !r0.Queued {
		t.Errorf("results[0] = %+v, want deploy -> req-deploy queued", r0)
	}
	if cap.count() != 1 {
		t.Errorf("enqueue count = %d, want 1", cap.count())
	}
}

func TestSendAsync_MultiChat_Expands(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "req-" + p.ChatID, nil
	}
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":"a,b","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 202 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 2 results", resp)
	}
	if resp.Results[0].Chat != "a" || resp.Results[0].RequestID != "req-a" || !resp.Results[0].Queued {
		t.Errorf("results[0] = %+v, want a -> req-a queued", resp.Results[0])
	}
	if resp.Results[1].Chat != "b" || resp.Results[1].RequestID != "req-b" || !resp.Results[1].Queued {
		t.Errorf("results[1] = %+v, want b -> req-b queued", resp.Results[1])
	}
	// Expand: one independent enqueue per chat, each carrying only its own chat.
	if cap.count() != 2 {
		t.Fatalf("enqueue count = %d, want 2 (one per chat)", cap.count())
	}
	seen := map[string]bool{}
	for _, c := range cap.calls {
		seen[c.ChatID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("enqueued chats = %v, want a and b", seen)
	}
}

func TestSendAsync_MultiChat_Dedup(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "req-" + p.ChatID, nil
	}
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":"a, a ,b,a","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 202 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if len(resp.Results) != 2 || resp.Results[0].Chat != "a" || resp.Results[1].Chat != "b" {
		t.Fatalf("results = %+v, want deduped a then b", resp.Results)
	}
	if cap.count() != 2 {
		t.Errorf("enqueue count = %d, want 2 (deduped)", cap.count())
	}
}

func TestSendAsync_MultiChat_PartialFailure(t *testing.T) {
	cap := &captureSend{}
	send := func(_ context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		if p.ChatID == "b" {
			return "", fmt.Errorf("broker refused chat b")
		}
		return "req-" + p.ChatID, nil
	}
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":"a,b","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 202 {
		t.Fatalf("status = %d, want 202 (partial), body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 1 {
		t.Fatalf("response = %+v, want ok with 1 result, 1 error", resp)
	}
	if resp.Results[0].Chat != "a" {
		t.Errorf("results[0].Chat = %q, want a", resp.Results[0].Chat)
	}
	if resp.Errors[0].Chat != "b" || !strings.Contains(resp.Errors[0].Error, "broker refused chat b") {
		t.Errorf("errors[0] = %+v, want b -> broker refused", resp.Errors[0])
	}
}

func TestSendAsync_MultiChat_AllFail(t *testing.T) {
	send := func(_ context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("broker down")
	}
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":"a,b","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (all failed), body: %s", w.Code, w.Body.String())
	}
	resp := parseMultiResponse(t, w)
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
		t.Fatalf("response = %+v, want not-ok with 0 results, 2 errors", resp)
	}
}

func TestSendAsync_EmptyAfterParse(t *testing.T) {
	// chat_id present but only commas -> request-level 400.
	send := func(_ context.Context, p *SendPayload) (string, error) { return "x", nil }
	srv := newAsyncSendServer(send)
	body := `{"bot":"alerts","chat_id":",,","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.Error != "chat_id is required" {
		t.Errorf("error = %q, want 'chat_id is required'", resp.Error)
	}
}

func TestSendSync_EmptyAfterParse(t *testing.T) {
	// chat_id present but only commas -> request-level 400 (not a per-chat error).
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":",,","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.Error != "chat_id is required" {
		t.Errorf("error = %q, want 'chat_id is required'", resp.Error)
	}
}
