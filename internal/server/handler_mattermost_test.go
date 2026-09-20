package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

const mattermostChatID = "054af49e-5e18-4dca-ad73-4f96b6de63fa"

const mattermostPostPayload = `{
  "channel_id": "054af49e-5e18-4dca-ad73-4f96b6de63fa",
  "message": "",
  "props": {"attachments": [{
    "fallback": "Disk almost full",
    "color": "#d9534f",
    "title": "[P2] Disk almost full",
    "text": "node-1 at 94%",
    "title_link": "http://alerts.example/42",
    "fields": [
      {"short": true, "title": "Status", "value": "firing"},
      {"short": true, "title": "Severity", "value": "critical"},
      {"short": true, "title": "Alert ID", "value": "42"}
    ],
    "actions": [{"id": "ack", "name": "Acknowledge"}]
  }]}
}`

type mattermostCall struct {
	sends  int
	edits  int
	sent   SendPayload
	edited EditPayload
}

func testMattermostConfig() *MattermostConfig {
	return &MattermostConfig{
		ErrorSeverities:   DefaultMattermostErrorSeverities,
		WarningSeverities: DefaultMattermostWarningSeverities,
		Icons:             DefaultMattermostIcons,
	}
}

func mattermostServer(t *testing.T, cfg *MattermostConfig, call *mattermostCall, opts ...Option) *Server {
	t.Helper()
	return newMattermostServer(t, Config{
		Listen: ":0", BasePath: "/api/v1",
		Keys: []ResolvedKey{{Name: "t", Key: "k"}},
	}, cfg, call, nil, nil, opts...)
}

func newMattermostServer(t *testing.T, srvCfg Config, cfg *MattermostConfig, call *mattermostCall, sendErr, editErr error, opts ...Option) *Server {
	t.Helper()
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		call.sends++
		call.sent = *p
		if sendErr != nil {
			return "", sendErr
		}
		return "sync-42", nil
	}
	base := []Option{
		WithMattermost(cfg),
		WithMessageEditor(func(ctx context.Context, p *EditPayload) error {
			call.edits++
			call.edited = *p
			return editErr
		}),
	}
	return New(srvCfg, sendFn, selfChatResolver(), append(base, opts...)...)
}

func parseMattermostPost(t *testing.T, w *httptest.ResponseRecorder) mattermostPostResponse {
	t.Helper()
	var resp mattermostPostResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

func mattermostPayload(attachments string) string {
	return fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[%s]}}`, mattermostChatID, attachments)
}

// TestRenderMattermostPost covers the body the gateway builds, including the
// state icon, without going through HTTP: the wiring is proved once by the
// happy path in TestMattermost_CreatePost.
func TestRenderMattermostPost(t *testing.T) {
	tests := []struct {
		name  string
		post  string
		icons map[string]string
		want  string
	}{
		{
			name: "title, text, fields and link are all delivered",
			post: mattermostPostPayload,
			want: "\U0001F534 [P2] Disk almost full\nnode-1 at 94%\n" +
				"Status: firing\nSeverity: critical\nAlert ID: 42\nhttp://alerts.example/42",
		},
		{
			name: "title and text alone",
			post: `{"props":{"attachments":[{"title":"T","text":"B"}]}}`,
			want: "\U0001F535 T\nB",
		},
		{
			name: "a non-empty message is kept ahead of the attachment",
			post: `{"message":"M","props":{"attachments":[{"title":"T"}]}}`,
			want: "M\n\n\U0001F535 T",
		},
		{
			name: "numeric field values survive",
			post: `{"props":{"attachments":[{"title":"T","fields":[{"title":"Alert ID","value":42}]}]}}`,
			want: "\U0001F535 T\nAlert ID: 42",
		},
		{
			name: "a field without a title contributes its bare value",
			post: `{"props":{"attachments":[{"title":"T","fields":[{"value":"bare"}]}]}}`,
			want: "\U0001F535 T\nbare",
		},
		{
			name: "empty and null field values are dropped",
			post: `{"props":{"attachments":[{"title":"T","fields":[{"title":"Team","value":""},{"title":"Service","value":null}]}]}}`,
			want: "\U0001F535 T",
		},
		{
			name: "an attachment with only buttons renders nothing",
			post: `{"props":{"attachments":[{"actions":[{"id":"ack"}]}]}}`,
			want: "",
		},
		{
			name: "each attachment is marked separately",
			post: `{"props":{"attachments":[
				{"title":"Firing","fields":[{"title":"Severity","value":"critical"}]},
				{"title":"Resolved","fields":[{"title":"Status","value":"resolved"}]}
			]}}`,
			want: "\U0001F534 Firing\nSeverity: critical\n\n\U0001F7E2 Resolved\nStatus: resolved",
		},
		{
			name:  "an empty icon map leaves lines bare",
			post:  `{"props":{"attachments":[{"title":"T","fields":[{"title":"Severity","value":"critical"}]}]}}`,
			icons: map[string]string{},
			want:  "T\nSeverity: critical",
		},
		{
			name:  "a custom icon map replaces the defaults",
			post:  `{"props":{"attachments":[{"title":"T","fields":[{"title":"Severity","value":"critical"}]}]}}`,
			icons: map[string]string{MattermostStateError: "\U0001F525"},
			want:  "\U0001F525 T\nSeverity: critical",
		},
		{
			name:  "a map without the matching state leaves the line bare",
			post:  `{"props":{"attachments":[{"title":"T","fields":[{"title":"Severity","value":"critical"}]}]}}`,
			icons: map[string]string{MattermostStateResolved: "\U0001F7E2"},
			want:  "T\nSeverity: critical",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var post MattermostPost
			if err := json.Unmarshal([]byte(tt.post), &post); err != nil {
				t.Fatalf("decode post: %v", err)
			}
			cfg := testMattermostConfig()
			if tt.icons != nil {
				cfg.Icons = tt.icons
			}
			if got := renderMattermostPost(post, cfg); got != tt.want {
				t.Errorf("render = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMattermostState pins the classification rules that drive both the BotX
// status and the icon.
func TestMattermostState(t *testing.T) {
	tests := []struct {
		name       string
		attachment string
		want       string
	}{
		{
			name:       "resolved wins over the severity",
			attachment: `{"fields":[{"title":"Status","value":"resolved"},{"title":"Severity","value":"critical"}]}`,
			want:       MattermostStateResolved,
		},
		{
			name:       "acknowledged wins over the severity",
			attachment: `{"fields":[{"title":"Status","value":"acknowledged"},{"title":"Severity","value":"critical"}]}`,
			want:       MattermostStateAcknowledged,
		},
		{
			name:       "an error severity while firing",
			attachment: `{"fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"critical"}]}`,
			want:       MattermostStateError,
		},
		{
			name:       "a warning severity is its own state",
			attachment: `{"fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"warning"}]}`,
			want:       MattermostStateWarning,
		},
		{
			name:       "an unlisted severity is neutral",
			attachment: `{"fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"info"}]}`,
			want:       MattermostStateDefault,
		},
		{
			name:       "field case and padding do not matter",
			attachment: `{"fields":[{"title":"  SEVERITY ","value":" CRITICAL "}]}`,
			want:       MattermostStateError,
		},
		{
			name:       "no usable fields is neutral",
			attachment: `{"fields":[{"title":"Team","value":"sre"},{"title":"Severity","value":"-"}]}`,
			want:       MattermostStateDefault,
		},
		{
			name:       "a colour never overrides the fields",
			attachment: `{"color":"#d9534f","fields":[{"title":"Status","value":"resolved"}]}`,
			want:       MattermostStateResolved,
		},
		{
			name:       "a colour alone decides nothing",
			attachment: `{"color":"#d9534f"}`,
			want:       MattermostStateDefault,
		},
	}

	cfg := testMattermostConfig()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var att MattermostAttachment
			if err := json.Unmarshal([]byte(tt.attachment), &att); err != nil {
				t.Fatalf("decode attachment: %v", err)
			}
			if got := cfg.state(att); got != tt.want {
				t.Errorf("state = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMattermost_CreatePost(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, testMattermostConfig(), &call)

	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if call.sends != 1 {
		t.Fatalf("sends = %d, want 1", call.sends)
	}
	wantMessage := "\U0001F534 [P2] Disk almost full\nnode-1 at 94%\n" +
		"Status: firing\nSeverity: critical\nAlert ID: 42\nhttp://alerts.example/42"
	if call.sent.Message != wantMessage {
		t.Errorf("message = %q, want %q", call.sent.Message, wantMessage)
	}
	if call.sent.Status != "error" {
		t.Errorf("BotX status = %q, want %q", call.sent.Status, "error")
	}
	if call.sent.ChatID != mattermostChatID {
		t.Errorf("delivered to %q, want %q", call.sent.ChatID, mattermostChatID)
	}
	resp := parseMattermostPost(t, w)
	if resp.ID != "sync-42" {
		t.Errorf("id = %q, want the sync_id %q", resp.ID, "sync-42")
	}
	if resp.ChannelID != mattermostChatID {
		t.Errorf("channel_id = %q, want the requested %q", resp.ChannelID, mattermostChatID)
	}
}

func TestMattermost_RequestErrorsDeliverNothing(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "invalid JSON", payload: `{bad`},
		{name: "no attachments and no message", payload: mattermostPayload("")},
		{name: "an attachment with only buttons", payload: mattermostPayload(`{"actions":[{"id":"ack"}]}`)},
		{name: "no channel_id and no default", payload: `{"props":{"attachments":[{"title":"T"}]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, testMattermostConfig(), &call)

			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(tt.payload), webhookHeaders())
			if w.Code != 400 {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			if call.sends != 0 {
				t.Errorf("sends = %d, want 0: a rejected request must deliver nothing", call.sends)
			}
		})
	}
}

func TestMattermost_ChatFallback(t *testing.T) {
	tests := []struct {
		name          string
		cfg           MattermostConfig
		globalDefault string
		want          string
	}{
		{
			name: "default_chat_id comes first",
			cfg:  MattermostConfig{DefaultChatID: "infra", FallbackChatID: "single"},
			want: "infra",
		},
		{
			name:          "then the global default chat",
			cfg:           MattermostConfig{FallbackChatID: "single"},
			globalDefault: "ops",
			want:          "ops",
		},
		{
			name: "then the sole configured alias",
			cfg:  MattermostConfig{FallbackChatID: "single"},
			want: "single",
		},
		{
			name: "nothing configured leaves no target",
			cfg:  MattermostConfig{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.singleChat(tt.globalDefault); got != tt.want {
				t.Errorf("singleChat = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMattermost_ChannelIDOverridesTheDefault(t *testing.T) {
	var call mattermostCall
	cfg := testMattermostConfig()
	cfg.DefaultChatID = "infra"
	srv := mattermostServer(t, cfg, &call)

	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if call.sent.ChatID != mattermostChatID {
		t.Errorf("chat = %q, want the channel_id from the body %q", call.sent.ChatID, mattermostChatID)
	}
}

func TestMattermost_UpdatePost(t *testing.T) {
	tests := []struct {
		name       string
		fields     string
		wantStatus string
		wantMsg    string
	}{
		{
			name:       "a resolve clears the error status and turns the icon green",
			fields:     `[{"title":"Status","value":"resolved"},{"title":"Severity","value":"critical"}]`,
			wantStatus: "ok",
			wantMsg:    "\U0001F7E2 RESOLVED: T\nStatus: resolved\nSeverity: critical",
		},
		{
			name:       "a still-firing update keeps the error status",
			fields:     `[{"title":"Status","value":"firing"},{"title":"Severity","value":"critical"}]`,
			wantStatus: "error",
			wantMsg:    "\U0001F534 RESOLVED: T\nStatus: firing\nSeverity: critical",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, testMattermostConfig(), &call)

			payload := mattermostPayload(fmt.Sprintf(`{"title":"RESOLVED: T","fields":%s}`, tt.fields))
			w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/sync-42", strings.NewReader(payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if call.edits != 1 || call.sends != 0 {
				t.Fatalf("edits = %d, sends = %d, want an edit in place and no new message", call.edits, call.sends)
			}
			if call.edited.SyncID != "sync-42" {
				t.Errorf("edited sync_id = %q, want the post_id from the path", call.edited.SyncID)
			}
			if call.edited.Message != tt.wantMsg {
				t.Errorf("edited message = %q, want %q", call.edited.Message, tt.wantMsg)
			}
			if call.edited.Status != tt.wantStatus {
				t.Errorf("edited status = %q, want %q", call.edited.Status, tt.wantStatus)
			}
			resp := parseMattermostPost(t, w)
			if resp.ID != "sync-42" || resp.ChannelID != mattermostChatID {
				t.Errorf("response = %+v, want the same id and channel_id", resp)
			}
		})
	}
}

func TestMattermost_UpdateFailures(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		editErr  error
		noEditor bool
		wantCode int
	}{
		{
			name:     "an upstream edit failure is a bad gateway",
			path:     "/api/v1/mattermost/api/v4/posts/sync-42",
			editErr:  errors.New("message too old to edit"),
			wantCode: 502,
		},
		{
			name:     "an unconfigured editor is not implemented",
			path:     "/api/v1/mattermost/api/v4/posts/sync-42",
			noEditor: true,
			wantCode: 501,
		},
		{
			name:     "a missing post_id does not route",
			path:     "/api/v1/mattermost/api/v4/posts/",
			wantCode: 404,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			opts := []Option{WithMattermost(testMattermostConfig())}
			if !tt.noEditor {
				opts = append(opts, WithMessageEditor(func(ctx context.Context, p *EditPayload) error {
					call.edits++
					return tt.editErr
				}))
			}
			srv := New(
				Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
				func(ctx context.Context, p *SendPayload) (string, error) { call.sends++; return "sync-42", nil },
				selfChatResolver(),
				opts...,
			)

			w := doRequest(srv, "PUT", tt.path, strings.NewReader(mattermostPostPayload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if call.sends != 0 {
				t.Errorf("sends = %d, want 0: a failed update must not post a new message", call.sends)
			}
			wantEdits := 0
			if tt.editErr != nil {
				wantEdits = 1
			}
			if call.edits != wantEdits {
				t.Errorf("edits = %d, want %d", call.edits, wantEdits)
			}
		})
	}
}

func TestMattermost_DeliveryFailuresAreBadGateway(t *testing.T) {
	tests := []struct {
		name    string
		sendErr error
	}{
		{name: "the upstream send failed", sendErr: errors.New("botx unavailable")},
		{name: "the upstream accepted without a sync_id", sendErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
				call.sends++
				return "", tt.sendErr
			}
			srv := New(
				Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
				sendFn, selfChatResolver(),
				WithMattermost(testMattermostConfig()),
			)

			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
			if w.Code != 502 {
				t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
			}
			if call.sends != 1 {
				t.Errorf("sends = %d, want 1: the failure path must be reached through a real attempt", call.sends)
			}
		})
	}
}

// TestMattermost_QueryKeyIsRefused proves the standard auth middleware is wired
// to both endpoints; the middleware itself is covered in server_test.go.
func TestMattermost_QueryKeyIsRefused(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: "POST", path: "/api/v1/mattermost/api/v4/posts?api_key=k"},
		{method: "PUT", path: "/api/v1/mattermost/api/v4/posts/sync-42?api_key=k"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, testMattermostConfig(), &call)

			w := doRequest(srv, tt.method, tt.path, strings.NewReader(mattermostPostPayload),
				map[string]string{"Content-Type": "application/json"})
			if w.Code != 401 {
				t.Fatalf("status = %d, want 401 (body: %s)", w.Code, w.Body.String())
			}
			if call.sends != 0 || call.edits != 0 {
				t.Errorf("sends = %d, edits = %d, want nothing to happen", call.sends, call.edits)
			}
		})
	}
}

func TestMattermost_KeyScope(t *testing.T) {
	const otherChat = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name      string
		method    string
		path      string
		channelID string
		wantCode  int
		wantSends int
		wantEdits int
	}{
		{
			name:      "a post to a chat inside the scope is delivered",
			method:    "POST",
			path:      "/api/v1/mattermost/api/v4/posts",
			channelID: mattermostChatID,
			wantCode:  200,
			wantSends: 1,
		},
		{
			name:      "a post to a chat outside the scope is refused",
			method:    "POST",
			path:      "/api/v1/mattermost/api/v4/posts",
			channelID: otherChat,
			wantCode:  403,
		},
		{
			name:      "an edit is refused even for a chat inside the scope",
			method:    "PUT",
			path:      "/api/v1/mattermost/api/v4/posts/sync-42",
			channelID: mattermostChatID,
			wantCode:  403,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := newMattermostServer(t, Config{
				Listen: ":0", BasePath: "/api/v1",
				Keys: []ResolvedKey{{Name: "t", Key: "k", Chats: []string{mattermostChatID}}},
			}, testMattermostConfig(), &call, nil, nil)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"T"}]}}`, tt.channelID)
			w := doRequest(srv, tt.method, tt.path, strings.NewReader(payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if call.sends != tt.wantSends || call.edits != tt.wantEdits {
				t.Errorf("sends = %d, edits = %d, want %d and %d", call.sends, call.edits, tt.wantSends, tt.wantEdits)
			}
		})
	}
}

func TestMattermost_UnscopedKeyCanEdit(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, testMattermostConfig(), &call)

	w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/sync-42", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if call.edits != 1 {
		t.Errorf("edits = %d, want 1", call.edits)
	}
}

func TestMattermost_RootPathIsNotServed(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, testMattermostConfig(), &call)

	w := doRequest(srv, "POST", "/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404: nothing may be served at the bare Mattermost path", w.Code)
	}
}
