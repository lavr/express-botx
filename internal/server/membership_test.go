package server

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMembership answers lookups from a fixed table keyed "bot/chat" and
// records every call that reached it.
type fakeMembership struct {
	mu      sync.Mutex
	members map[string]bool
	err     error
	calls   []string
}

func (f *fakeMembership) lookup(_ context.Context, bot, chatID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, bot+"/"+chatID)
	if f.err != nil {
		return false, f.err
	}
	return f.members[bot+"/"+chatID], nil
}

func (f *fakeMembership) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeMembership) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

var memberKey = ResolvedKey{Name: "controller", Key: "member", ChatsWhereMember: true}

type memberServerOpts struct {
	botNames    []string
	defaultChat string
	chatBot     string // bot bound to own-chat
	sent        *[]SendPayload
	onResolve   func(chatID string)  // called on every chat resolution
	onSend      func(p *SendPayload) // called after each send
}

// newMemberServer is newScopeServer with a membership-scoped key and the given
// lookup wired in.
func newMemberServer(f *fakeMembership, o memberServerOpts, srvOpts ...Option) *Server {
	aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{memberKey, {Name: "open", Key: "open"}},
		BotNames:         o.botNames,
		DefaultChatAlias: o.defaultChat,
	}
	var mu sync.Mutex
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		if o.sent != nil {
			mu.Lock()
			*o.sent = append(*o.sent, *p)
			mu.Unlock()
		}
		if o.onSend != nil {
			o.onSend(p)
		}
		return "sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if o.onResolve != nil {
			o.onResolve(chatID)
		}
		id, ok := aliases[chatID]
		if !ok && (chatID == ownUUID || chatID == otherUUID) {
			id, ok = chatID, true
		}
		if !ok {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q, available: other-chat, own-chat", chatID)
		}
		res := ChatResolveResult{ChatID: id}
		if id == ownUUID {
			res.Bot = o.chatBot
		}
		return res, nil
	}
	if f != nil {
		srvOpts = append(srvOpts, WithChatMembership(f.lookup, MembershipOptions{}))
	}
	return New(cfg, sendFn, chatResolver, srvOpts...)
}

func sendMemberBody(srv *Server, key, body string) *httptest.ResponseRecorder {
	return doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + key,
	})
}

func TestMembershipScope_Send(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
	srv := newMemberServer(f, memberServerOpts{})

	tests := []struct {
		name   string
		chatID string
		want   int
	}{
		{"member chat by uuid", ownUUID, 200},
		{"member chat by alias", "own-chat", 200},
		{"chat without the bot", otherUUID, 403},
		{"chat without the bot by alias", "other-chat", 403},
		{"unknown alias", "no-such-chat", 403},
		{"one foreign chat refuses the whole fan-out", ownUUID + "," + otherUUID, 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := sendAs(srv, "member", tc.chatID)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "available") {
				t.Errorf("membership key was shown the alias catalog: %s", w.Body.String())
			}
		})
	}
}

func TestMembershipScope_SendBotXFailureIs503(t *testing.T) {
	f := &fakeMembership{err: errors.New("dial tcp: connection refused")}
	var sent []SendPayload
	srv := newMemberServer(f, memberServerOpts{sent: &sent})

	w := sendAs(srv, "member", ownUUID)
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503 (body: %s)", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("503 without Retry-After")
	}
	if strings.Contains(w.Body.String(), "connection refused") {
		t.Errorf("cause leaked to the client: %s", w.Body.String())
	}
	if len(sent) != 0 {
		t.Errorf("message sent without a membership check: %+v", sent)
	}
}

func TestMembershipScope_NotConfiguredIs503(t *testing.T) {
	srv := newMemberServer(nil, memberServerOpts{})
	if w := sendAs(srv, "member", ownUUID); w.Code != 503 {
		t.Errorf("status = %d, want 503 (body: %s)", w.Code, w.Body.String())
	}
}

func TestMembershipScope_OtherKeysUnaffected(t *testing.T) {
	f := &fakeMembership{}
	srv := newMemberServer(f, memberServerOpts{})
	if w := sendAs(srv, "open", otherUUID); w.Code != 200 {
		t.Errorf("unscoped key: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if n := f.callCount(); n != 0 {
		t.Errorf("unscoped key triggered %d membership lookups", n)
	}
}

func TestMembershipScope_Alertmanager(t *testing.T) {
	body := alertmanagerPayload("firing", AlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})
	post := func(srv *Server, query string) *httptest.ResponseRecorder {
		return doRequest(srv, "POST", "/api/v1/alertmanager"+query, strings.NewReader(body),
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer member"})
	}

	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
	amCfg := testAlertmanagerConfig(t)
	amCfg.DefaultChatID = ownUUID
	srv := newMemberServer(f, memberServerOpts{}, WithAlertmanager(amCfg))

	if w := post(srv, ""); w.Code != 200 {
		t.Errorf("default chat with the bot: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if w := post(srv, "?chat_id="+otherUUID); w.Code != 403 {
		t.Errorf("chat without the bot: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}

	down := &fakeMembership{err: errors.New("botx: HTTP 502")}
	srv = newMemberServer(down, memberServerOpts{}, WithAlertmanager(amCfg))
	w := post(srv, "")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Errorf("BotX down: status = %d, Retry-After = %q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestMembershipScope_Grafana(t *testing.T) {
	body := grafanaPayload("firing", "alerting", "[FIRING:1] HighCPU", GrafanaAlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})
	post := func(srv *Server, query string) *httptest.ResponseRecorder {
		return doRequest(srv, "POST", "/api/v1/grafana"+query, strings.NewReader(body),
			map[string]string{"Content-Type": "application/json", "X-API-Key": "member"})
	}

	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
	grCfg := testGrafanaConfig(t)
	grCfg.DefaultChatID = ownUUID
	srv := newMemberServer(f, memberServerOpts{}, WithGrafana(grCfg))

	if w := post(srv, ""); w.Code != 200 {
		t.Errorf("default chat with the bot: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if w := post(srv, "?chat_id=other-chat"); w.Code != 403 {
		t.Errorf("chat without the bot: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}

	down := &fakeMembership{err: context.DeadlineExceeded}
	srv = newMemberServer(down, memberServerOpts{}, WithGrafana(grCfg))
	w := post(srv, "")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Errorf("BotX timeout: status = %d, Retry-After = %q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
}

// The server's default chat is a target like any other: a membership key
// omitting chat_id does not get to send there unless the bot is a member.
func TestMembershipScope_DefaultChatIsChecked(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{}}
	srv := newMemberServer(f, memberServerOpts{defaultChat: "own-chat"})

	if w := sendMemberBody(srv, "member", `{"message":"hi"}`); w.Code != 403 {
		t.Errorf("default chat without the bot: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
	f.members["/"+ownUUID] = true
	srv = newMemberServer(f, memberServerOpts{defaultChat: "own-chat"})
	if w := sendMemberBody(srv, "member", `{"message":"hi"}`); w.Code != 200 {
		t.Errorf("default chat with the bot: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// In a multi-bot setup the bot checked is the one that sends: the chat's bound
// bot unless the request names another.
func TestMembershipScope_MultiBotChecksTheSendingBot(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"alpha/" + ownUUID: true}}
	var sent []SendPayload
	o := memberServerOpts{botNames: []string{"alpha", "beta"}, chatBot: "beta", sent: &sent}
	srv := newMemberServer(f, o)

	if w := sendAs(srv, "member", "own-chat"); w.Code != 403 {
		t.Errorf("chat-bound bot not a member: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
	w := sendMemberBody(srv, "member", fmt.Sprintf(`{"chat_id":%q,"bot":"alpha","message":"hi"}`, ownUUID))
	if w.Code != 200 {
		t.Fatalf("requested bot is a member: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if len(sent) != 1 || sent[0].Bot != "alpha" {
		t.Errorf("sent = %+v, want one message from alpha", sent)
	}
	if w := sendMemberBody(srv, "member", fmt.Sprintf(`{"chat_id":%q,"bot":"beta","message":"hi"}`, ownUUID)); w.Code != 403 {
		t.Errorf("requested bot not a member: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
}

// The gate on the send path refuses a membership-scoped send on its own, so a
// chat dropped between the up-front check and delivery is not sent to.
func TestMembershipScope_SendPathRechecks(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{}}
	var sent []SendPayload
	srv := newMemberServer(f, memberServerOpts{sent: &sent})

	ctx := context.WithValue(context.Background(), keyMemberScopeKey, true)
	_, err := srv.send(ctx, &SendPayload{ChatID: ownUUID, Message: "hi"})
	if !errors.Is(err, ErrChatNotAllowed) {
		t.Fatalf("err = %v, want ErrChatNotAllowed", err)
	}
	f.setErr(errors.New("botx: HTTP 503"))
	_, err = srv.send(ctx, &SendPayload{ChatID: otherUUID, Message: "hi"})
	if !errors.Is(err, ErrMembershipUnavailable) {
		t.Fatalf("err = %v, want ErrMembershipUnavailable", err)
	}
	if len(sent) != 0 {
		t.Errorf("sent despite the gate: %+v", sent)
	}
}

func TestMembershipChecker_Cache(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
	m := newMembershipChecker(f.lookup, MembershipOptions{TTL: time.Minute, NegativeTTL: 10 * time.Second})
	now := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return now }
	ctx := context.Background()

	check := func(chat string, wantMember, wantCached bool) {
		t.Helper()
		member, cached, err := m.check(ctx, "", chat)
		if err != nil || member != wantMember || cached != wantCached {
			t.Fatalf("check(%s) = %v, cached %v, err %v; want %v, cached %v", chat, member, cached, err, wantMember, wantCached)
		}
	}

	check(ownUUID, true, false)
	check(strings.ToUpper(ownUUID), true, true) // UUID case does not matter
	check(otherUUID, false, false)
	check(otherUUID, false, true)
	if n := f.callCount(); n != 2 {
		t.Fatalf("lookups = %d, want 2", n)
	}

	now = now.Add(11 * time.Second) // past the negative TTL only
	check(otherUUID, false, false)
	check(ownUUID, true, true)

	now = now.Add(time.Minute) // past both
	check(ownUUID, true, false)
	if n := f.callCount(); n != 4 {
		t.Fatalf("lookups = %d, want 4", n)
	}
}

func TestMembershipChecker_FailureIsNotCached(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}, err: errors.New("boom")}
	m := newMembershipChecker(f.lookup, MembershipOptions{})
	if _, _, err := m.check(context.Background(), "", ownUUID); err == nil {
		t.Fatal("want an error from a failed lookup")
	}
	f.setErr(nil)
	member, cached, err := m.check(context.Background(), "", ownUUID)
	if err != nil || !member || cached {
		t.Fatalf("after recovery: member %v, cached %v, err %v; want a fresh positive answer", member, cached, err)
	}
}

// Bot names are case-sensitive in the config, so Ops and ops are two bots.
func TestMembershipChecker_BotNameCaseMatters(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"Ops/" + ownUUID: true}}
	m := newMembershipChecker(f.lookup, MembershipOptions{})
	ctx := context.Background()
	if member, _, _ := m.check(ctx, "Ops", ownUUID); !member {
		t.Fatal("Ops should be a member")
	}
	if member, cached, _ := m.check(ctx, "ops", ownUUID); member || cached {
		t.Fatalf("ops got Ops's answer: member %v, cached %v", member, cached)
	}
}

// blockingLookup holds every call until released, counting calls per bot.
type blockingLookup struct {
	release chan struct{}
	started chan string
	calls   atomic.Int32
}

func newBlockingLookup() *blockingLookup {
	return &blockingLookup{release: make(chan struct{}), started: make(chan string, 64)}
}

func (b *blockingLookup) lookup(ctx context.Context, bot, chatID string) (bool, error) {
	b.calls.Add(1)
	b.started <- bot
	select {
	case <-b.release:
		return bot == "Ops", nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func TestMembershipChecker_ConcurrentChecksShareOneLookup(t *testing.T) {
	b := newBlockingLookup()
	m := newMembershipChecker(b.lookup, MembershipOptions{})

	type result struct {
		bot    string
		member bool
		err    error
	}
	results := make(chan result, 20)
	for i := 0; i < 20; i++ {
		bot := "Ops"
		if i%2 == 1 {
			bot = "ops"
		}
		go func() {
			member, _, err := m.check(context.Background(), bot, ownUUID)
			results <- result{bot, member, err}
		}()
	}
	<-b.started
	<-b.started
	waitFor(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.inflight) == 2
	})
	close(b.release)
	for i := 0; i < 20; i++ {
		r := <-results
		if r.err != nil || r.member != (r.bot == "Ops") {
			t.Errorf("%s: member %v, err %v", r.bot, r.member, r.err)
		}
	}
	if n := b.calls.Load(); n != 2 {
		t.Errorf("lookups = %d, want one per bot", n)
	}
}

// A request that gives up does not fail the others waiting on the same chat.
func TestMembershipChecker_CancelledWaiterLeavesOthers(t *testing.T) {
	b := newBlockingLookup()
	m := newMembershipChecker(b.lookup, MembershipOptions{})

	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, _, err := m.check(first, "Ops", ownUUID)
		firstErr <- err
	}()
	<-b.started

	second := make(chan bool, 1)
	go func() {
		member, _, _ := m.check(context.Background(), "Ops", ownUUID)
		second <- member
	}()

	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: err = %v, want context.Canceled", err)
	}
	close(b.release)
	if !<-second {
		t.Fatal("the remaining waiter lost the answer")
	}
	if n := b.calls.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
}

func TestMembershipChecker_InflightLimit(t *testing.T) {
	b := newBlockingLookup()
	m := newMembershipChecker(b.lookup, MembershipOptions{})
	defer close(b.release)

	for i := 0; i < membershipMaxInflight; i++ {
		chat := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		go func() { _, _, _ = m.check(context.Background(), "Ops", chat) }()
		<-b.started
	}
	_, _, err := m.check(context.Background(), "Ops", ownUUID)
	if !errors.Is(err, errMembershipBusy) {
		t.Fatalf("err = %v, want errMembershipBusy", err)
	}
}

func TestMembershipChecker_CacheIsBounded(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{}}
	m := newMembershipChecker(f.lookup, MembershipOptions{})
	for i := 0; i < membershipMaxEntries+10; i++ {
		_, _, _ = m.check(context.Background(), "", fmt.Sprintf("00000000-0000-0000-0000-%012d", i))
	}
	m.mu.Lock()
	n := len(m.cache)
	m.mu.Unlock()
	if n > membershipMaxEntries {
		t.Fatalf("cache holds %d entries, limit %d", n, membershipMaxEntries)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

// testClock is a membership clock the test moves by hand.
type testClock struct{ ns atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *testClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func withClock(srv *Server) *testClock {
	c := &testClock{}
	c.ns.Store(time.Unix(1_000_000, 0).UnixNano())
	srv.membership.now = c.now
	return c
}

// The re-check right before sending keeps its HTTP meaning: a single chat whose
// cached answer expired between the up-front check and delivery, and whose
// fresh lookup fails, is 503 (nothing was sent, retrying is safe); one whose
// bot was removed meanwhile is 403.
func TestMembershipScope_RecheckBeforeSendKeepsStatus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(f *fakeMembership)
		want  int
	}{
		{"lookup fails at the re-check", func(f *fakeMembership) { f.setErr(errors.New("botx: HTTP 503")) }, 503},
		{"bot removed before the re-check", func(f *fakeMembership) {
			f.mu.Lock()
			f.members["/"+ownUUID] = false
			f.mu.Unlock()
		}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
			var sent []SendPayload
			var clock *testClock
			resolves := 0
			o := memberServerOpts{sent: &sent, onResolve: func(string) {
				// The first resolution is the up-front check, the second the
				// delivery: the cache expires in between.
				resolves++
				if resolves == 2 {
					clock.advance(2 * DefaultMembershipTTL)
					tc.after(f)
				}
			}}
			srv := newMemberServer(f, o)
			clock = withClock(srv)

			w := sendAs(srv, "member", ownUUID)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 503 && w.Header().Get("Retry-After") == "" {
				t.Error("503 without Retry-After")
			}
			if len(sent) != 0 {
				t.Errorf("sent despite the failed re-check: %+v", sent)
			}
		})
	}
}

// When the re-check fails for a later chat after an earlier one was delivered,
// the request reports a partial delivery: the delivered chat in results, the
// refused one in errors, as for any other partial failure.
func TestMembershipScope_RecheckFailsAfterPartialDelivery(t *testing.T) {
	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true, "/" + otherUUID: true}}
	var sent []SendPayload
	var clock *testClock
	o := memberServerOpts{sent: &sent, onSend: func(p *SendPayload) {
		if p.ChatID == ownUUID {
			clock.advance(2 * DefaultMembershipTTL)
			f.setErr(errors.New("botx: HTTP 503"))
		}
	}}
	srv := newMemberServer(f, o)
	clock = withClock(srv)

	w := sendAs(srv, "member", ownUUID+","+otherUUID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if len(sent) != 1 || sent[0].ChatID != ownUUID {
		t.Fatalf("sent = %+v, want only the first chat", sent)
	}
	if !strings.Contains(w.Body.String(), `"errors":[{"chat":"`+otherUUID) {
		t.Errorf("refused chat missing from errors: %s", w.Body.String())
	}
}
