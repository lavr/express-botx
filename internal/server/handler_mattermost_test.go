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
    "title_link": "http://ir/alerts/42",
    "fields": [
      {"short": true, "title": "Team", "value": "sre"},
      {"short": true, "title": "Severity", "value": "critical"},
      {"short": true, "title": "Alert ID", "value": "42"}
    ],
    "actions": [{"id": "ack", "name": "Acknowledge"}]
  }]}
}`

type mattermostCall struct {
	sent   SendPayload
	edited EditPayload
}

func mattermostServer(t *testing.T, cfg *MattermostConfig, call *mattermostCall, opts ...Option) *Server {
	t.Helper()
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		call.sent = *p
		return "sync-42", nil
	}
	base := []Option{
		WithMattermost(cfg),
		WithMessageEditor(func(ctx context.Context, p *EditPayload) error {
			call.edited = *p
			return nil
		}),
	}
	return New(
		Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
		sendFn, selfChatResolver(),
		append(base, opts...)...,
	)
}

func parseMattermostPost(t *testing.T, w *httptest.ResponseRecorder) mattermostPostResponse {
	t.Helper()
	var resp mattermostPostResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

func TestMattermost_CreatePostRendersAttachment(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantCode    int
		wantMessage string
	}{
		{
			name:     "title, text, fields and link are all delivered",
			payload:  mattermostPostPayload,
			wantCode: 200,
			wantMessage: "[P2] Disk almost full\nnode-1 at 94%\n" +
				"Team: sre\nSeverity: critical\nAlert ID: 42\nhttp://ir/alerts/42",
		},
		{
			name:        "attachment without fields keeps title and text",
			payload:     `{"channel_id":"c","message":"","props":{"attachments":[{"title":"T","text":"B"}]}}`,
			wantCode:    200,
			wantMessage: "T\nB",
		},
		{
			name:        "title alone is enough",
			payload:     `{"channel_id":"c","message":"","props":{"attachments":[{"title":"T"}]}}`,
			wantCode:    200,
			wantMessage: "T",
		},
		{
			name:        "a non-empty message is kept ahead of the attachment",
			payload:     `{"channel_id":"c","message":"M","props":{"attachments":[{"title":"T"}]}}`,
			wantCode:    200,
			wantMessage: "M\n\nT",
		},
		{
			name:        "numeric field values survive",
			payload:     `{"channel_id":"c","props":{"attachments":[{"title":"T","fields":[{"title":"Alert ID","value":42}]}]}}`,
			wantCode:    200,
			wantMessage: "T\nAlert ID: 42",
		},
		{
			name:        "a field without a title contributes its bare value",
			payload:     `{"channel_id":"c","props":{"attachments":[{"title":"T","fields":[{"value":"bare"}]}]}}`,
			wantCode:    200,
			wantMessage: "T\nbare",
		},
		{
			name:        "empty field values are dropped",
			payload:     `{"channel_id":"c","props":{"attachments":[{"title":"T","fields":[{"title":"Team","value":""},{"title":"Service","value":null}]}]}}`,
			wantCode:    200,
			wantMessage: "T",
		},
		{
			name:     "an empty post carries no content",
			payload:  `{"channel_id":"c","message":"","props":{"attachments":[]}}`,
			wantCode: 400,
		},
		{
			name:     "an attachment with only buttons carries no content",
			payload:  `{"channel_id":"c","props":{"attachments":[{"actions":[{"id":"ack"}]}]}}`,
			wantCode: 400,
		},
		{
			name:     "invalid JSON is refused",
			payload:  `{bad`,
			wantCode: 400,
		},
		{
			name:     "no channel_id and no default is refused",
			payload:  `{"props":{"attachments":[{"title":"T"}]}}`,
			wantCode: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, &MattermostConfig{ErrorSeverities: DefaultMattermostErrorSeverities, WarningSeverities: DefaultMattermostWarningSeverities}, &call)

			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(tt.payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode != 200 {
				return
			}
			if call.sent.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", call.sent.Message, tt.wantMessage)
			}
		})
	}
}

func TestMattermost_CreatePostResponse(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, &MattermostConfig{ErrorSeverities: DefaultMattermostErrorSeverities, WarningSeverities: DefaultMattermostWarningSeverities}, &call)

	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	resp := parseMattermostPost(t, w)
	if resp.ID != "sync-42" {
		t.Errorf("id = %q, want the sync_id %q", resp.ID, "sync-42")
	}
	if resp.ChannelID != mattermostChatID {
		t.Errorf("channel_id = %q, want the requested %q", resp.ChannelID, mattermostChatID)
	}
	if call.sent.ChatID != mattermostChatID {
		t.Errorf("delivered to %q, want %q", call.sent.ChatID, mattermostChatID)
	}
}

func TestMattermost_ChatTarget(t *testing.T) {
	tests := []struct {
		name          string
		payload       string
		defaultChatID string
		globalDefault string
		wantCode      int
		wantChat      string
	}{
		{
			name:     "channel_id addresses the chat",
			payload:  mattermostPostPayload,
			wantCode: 200,
			wantChat: mattermostChatID,
		},
		{
			name:          "an absent channel_id falls back to default_chat_id",
			payload:       `{"props":{"attachments":[{"title":"T"}]}}`,
			defaultChatID: "infra",
			wantCode:      200,
			wantChat:      "infra",
		},
		{
			name:          "an empty channel_id falls back to the global default chat",
			payload:       `{"channel_id":"","props":{"attachments":[{"title":"T"}]}}`,
			globalDefault: "ops",
			wantCode:      200,
			wantChat:      "ops",
		},
		{
			name:          "channel_id wins over the configured default",
			payload:       mattermostPostPayload,
			defaultChatID: "infra",
			wantCode:      200,
			wantChat:      mattermostChatID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
				call.sent = *p
				return "sync-42", nil
			}
			srv := New(
				Config{
					Listen: ":0", BasePath: "/api/v1",
					Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
					DefaultChatAlias: tt.globalDefault,
				},
				sendFn, selfChatResolver(),
				WithMattermost(&MattermostConfig{DefaultChatID: tt.defaultChatID}),
			)

			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(tt.payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if call.sent.ChatID != tt.wantChat {
				t.Errorf("chat = %q, want %q", call.sent.ChatID, tt.wantChat)
			}
		})
	}
}

func TestMattermost_State(t *testing.T) {
	tests := []struct {
		name       string
		attachment string
		wantStatus string
		wantIcon   string
	}{
		{
			name:       "a critical alert is an error",
			attachment: `{"title":"T","fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"critical"}]}`,
			wantStatus: "error",
			wantIcon:   "\U0001F534",
		},
		{
			name:       "resolved wins over the severity",
			attachment: `{"title":"T","fields":[{"title":"Status","value":"resolved"},{"title":"Severity","value":"critical"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F7E2",
		},
		{
			name:       "acknowledged wins over the severity",
			attachment: `{"title":"T","fields":[{"title":"Status","value":"acknowledged"},{"title":"Severity","value":"critical"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F7E1",
		},
		{
			name:       "a warning severity is its own state, distinct from acknowledged",
			attachment: `{"title":"T","fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"warning"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F7E0",
		},
		{
			name:       "an info severity stays neutral, distinct from warning",
			attachment: `{"title":"T","fields":[{"title":"Status","value":"firing"},{"title":"Severity","value":"info"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F535",
		},
		{
			name:       "field case does not matter",
			attachment: `{"title":"T","fields":[{"title":"SEVERITY","value":"CRITICAL"}]}`,
			wantStatus: "error",
			wantIcon:   "\U0001F534",
		},
		{
			name:       "a placeholder severity is neutral",
			attachment: `{"title":"T","fields":[{"title":"Severity","value":"-"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F535",
		},
		{
			name:       "a colour alone no longer decides anything",
			attachment: `{"title":"T","color":"#d9534f"}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F535",
		},
		{
			name:       "fields decide even against a contradicting colour",
			attachment: `{"title":"T","color":"#2e7d32","fields":[{"title":"Severity","value":"critical"}]}`,
			wantStatus: "error",
			wantIcon:   "\U0001F534",
		},
		{
			name:       "an attachment with no usable fields is neutral",
			attachment: `{"title":"T","fields":[{"title":"Team","value":"sre"}]}`,
			wantStatus: "ok",
			wantIcon:   "\U0001F535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, &MattermostConfig{
				ErrorSeverities:   DefaultMattermostErrorSeverities,
				WarningSeverities: DefaultMattermostWarningSeverities,
				Icons:             DefaultMattermostIcons,
			}, &call)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[%s]}}`, mattermostChatID, tt.attachment)
			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if call.sent.Status != tt.wantStatus {
				t.Errorf("BotX status = %q, want %q", call.sent.Status, tt.wantStatus)
			}
			if !strings.HasPrefix(call.sent.Message, tt.wantIcon+" ") {
				t.Errorf("message = %q, want it to lead with %q", call.sent.Message, tt.wantIcon)
			}
		})
	}
}

func TestMattermost_UpdatePost(t *testing.T) {
	const resolved = `{
	  "id": "sync-42",
	  "channel_id": "054af49e-5e18-4dca-ad73-4f96b6de63fa",
	  "message": "",
	  "props": {"attachments": [{"color":"#2e7d32","title":"RESOLVED: Disk almost full","text":"The alert has been resolved."}]}
	}`

	var call mattermostCall
	srv := mattermostServer(t, &MattermostConfig{ErrorSeverities: DefaultMattermostErrorSeverities, WarningSeverities: DefaultMattermostWarningSeverities}, &call)

	w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/sync-42", strings.NewReader(resolved), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if call.sent.Message != "" {
		t.Errorf("an update sent a new message %q, want an edit instead", call.sent.Message)
	}
	if call.edited.SyncID != "sync-42" {
		t.Errorf("edited sync_id = %q, want the post_id from the path %q", call.edited.SyncID, "sync-42")
	}
	want := "RESOLVED: Disk almost full\nThe alert has been resolved."
	if call.edited.Message != want {
		t.Errorf("edited message = %q, want %q", call.edited.Message, want)
	}
	resp := parseMattermostPost(t, w)
	if resp.ID != "sync-42" || resp.ChannelID != mattermostChatID {
		t.Errorf("response = %+v, want the same id and channel_id", resp)
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
			name:     "a missing post_id does not reach the editor",
			path:     "/api/v1/mattermost/api/v4/posts/",
			wantCode: 404,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			opts := []Option{WithMattermost(&MattermostConfig{})}
			if !tt.noEditor {
				opts = append(opts, WithMessageEditor(func(ctx context.Context, p *EditPayload) error {
					call.edited = *p
					return tt.editErr
				}))
			}
			srv := New(
				Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
				func(ctx context.Context, p *SendPayload) (string, error) { call.sent = *p; return "sync-42", nil },
				selfChatResolver(),
				opts...,
			)

			w := doRequest(srv, "PUT", tt.path, strings.NewReader(mattermostPostPayload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if call.sent.Message != "" {
				t.Errorf("a failed update sent a new message %q, want none", call.sent.Message)
			}
		})
	}
}

func TestMattermost_SendFailureIsBadGateway(t *testing.T) {
	var call mattermostCall
	srv := New(
		Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
		func(ctx context.Context, p *SendPayload) (string, error) {
			call.sent = *p
			return "", errors.New("botx unavailable")
		},
		selfChatResolver(),
		WithMattermost(&MattermostConfig{}),
	)

	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
	}
}

func TestMattermost_Auth(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		wantCode int
	}{
		{
			name:     "a bearer token authenticates",
			headers:  map[string]string{"Authorization": "Bearer k", "Content-Type": "application/json"},
			wantCode: 200,
		},
		{
			name:     "an api key header authenticates",
			headers:  map[string]string{"X-API-Key": "k", "Content-Type": "application/json"},
			wantCode: 200,
		},
		{
			name:     "no credentials are unauthorized",
			headers:  map[string]string{"Content-Type": "application/json"},
			wantCode: 401,
		},
		{
			name:     "a wrong bearer token is forbidden",
			headers:  map[string]string{"Authorization": "Bearer nope", "Content-Type": "application/json"},
			wantCode: 403,
		},
		{
			name:     "the key is not accepted from the query string",
			headers:  map[string]string{"Content-Type": "application/json"},
			wantCode: 401,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, &MattermostConfig{}, &call)

			path := "/api/v1/mattermost/api/v4/posts"
			if tt.name == "the key is not accepted from the query string" {
				path += "?api_key=k"
			}
			w := doRequest(srv, "POST", path, strings.NewReader(mattermostPostPayload), tt.headers)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestMattermost_ScopedKey(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		wantCode  int
	}{
		{name: "a chat inside the scope is delivered", channelID: mattermostChatID, wantCode: 200},
		{name: "a chat outside the scope is refused", channelID: "11111111-1111-1111-1111-111111111111", wantCode: 403},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			sent := 0
			srv := New(
				Config{
					Listen: ":0", BasePath: "/api/v1",
					Keys: []ResolvedKey{{Name: "t", Key: "k", Chats: []string{mattermostChatID}}},
				},
				func(ctx context.Context, p *SendPayload) (string, error) {
					sent++
					call.sent = *p
					return "sync-42", nil
				},
				selfChatResolver(),
				WithMattermost(&MattermostConfig{}),
			)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"T"}]}}`, tt.channelID)
			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == 200 && sent != 1 {
				t.Errorf("sends = %d, want 1", sent)
			}
			if tt.wantCode == 403 && sent != 0 {
				t.Errorf("sends = %d, want 0", sent)
			}
		})
	}
}

func TestMattermost_RootPathIsNotServed(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, &MattermostConfig{}, &call)

	w := doRequest(srv, "POST", "/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404: nothing may be served at the bare Mattermost path", w.Code)
	}
}

func TestMattermost_EmptySyncIDIsBadGateway(t *testing.T) {
	var call mattermostCall
	srv := New(
		Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
		func(ctx context.Context, p *SendPayload) (string, error) { call.sent = *p; return "", nil },
		selfChatResolver(),
		WithMattermost(&MattermostConfig{}),
	)

	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(mattermostPostPayload), webhookHeaders())
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502: a post with no sync_id could never be updated (body: %s)", w.Code, w.Body.String())
	}
	if call.sent.Message == "" {
		t.Error("the send was never attempted, so the test does not exercise the empty sync_id path")
	}
}

func TestMattermost_ScopedKeyCannotEdit(t *testing.T) {
	tests := []struct {
		name      string
		scope     []string
		channelID string
		wantCode  int
		wantEdits int
	}{
		{
			name:      "an unscoped key edits",
			channelID: mattermostChatID,
			wantCode:  200,
			wantEdits: 1,
		},
		{
			name:      "a scoped key is refused even for a chat inside its scope",
			scope:     []string{mattermostChatID},
			channelID: mattermostChatID,
			wantCode:  403,
			wantEdits: 0,
		},
		{
			name:      "a scoped key cannot reach a post in another chat by naming its own",
			scope:     []string{mattermostChatID},
			channelID: mattermostChatID,
			wantCode:  403,
			wantEdits: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edits := 0
			srv := New(
				Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k", Chats: tt.scope}}},
				func(ctx context.Context, p *SendPayload) (string, error) { return "sync-42", nil },
				selfChatResolver(),
				WithMattermost(&MattermostConfig{}),
				WithMessageEditor(func(ctx context.Context, p *EditPayload) error { edits++; return nil }),
			)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"RESOLVED"}]}}`, tt.channelID)
			w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/someone-elses-sync-id", strings.NewReader(payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if edits != tt.wantEdits {
				t.Errorf("edits = %d, want %d", edits, tt.wantEdits)
			}
		})
	}
}

func TestMattermost_UpdateCarriesStatus(t *testing.T) {
	tests := []struct {
		name       string
		fields     string
		wantStatus string
	}{
		{
			name:       "the resolve clears the error status",
			fields:     `[{"title":"Status","value":"resolved"},{"title":"Severity","value":"critical"}]`,
			wantStatus: "ok",
		},
		{
			name:       "a still-firing update keeps the error status",
			fields:     `[{"title":"Status","value":"firing"},{"title":"Severity","value":"critical"}]`,
			wantStatus: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, &MattermostConfig{
				ErrorSeverities:   DefaultMattermostErrorSeverities,
				WarningSeverities: DefaultMattermostWarningSeverities,
			}, &call)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"RESOLVED: T","fields":%s}]}}`, mattermostChatID, tt.fields)
			w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/sync-42", strings.NewReader(payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if call.edited.Status != tt.wantStatus {
				t.Errorf("edited status = %q, want %q", call.edited.Status, tt.wantStatus)
			}
		})
	}
}

func TestMattermost_IconConfig(t *testing.T) {
	tests := []struct {
		name        string
		icons       map[string]string
		wantMessage string
	}{
		{
			name:        "the defaults mark a critical alert red",
			icons:       DefaultMattermostIcons,
			wantMessage: "\U0001F534 T\nB\nSeverity: critical",
		},
		{
			name:        "an empty map disables icons",
			icons:       map[string]string{},
			wantMessage: "T\nB\nSeverity: critical",
		},
		{
			name:        "a custom map replaces the defaults",
			icons:       map[string]string{MattermostStateError: "\U0001F525"},
			wantMessage: "\U0001F525 T\nB\nSeverity: critical",
		},
		{
			name:        "a map without the matching state leaves the line bare",
			icons:       map[string]string{MattermostStateResolved: "\U0001F7E2"},
			wantMessage: "T\nB\nSeverity: critical",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call mattermostCall
			srv := mattermostServer(t, &MattermostConfig{
				ErrorSeverities:   DefaultMattermostErrorSeverities,
				WarningSeverities: DefaultMattermostWarningSeverities,
				Icons:             tt.icons,
			}, &call)

			payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"T","text":"B","fields":[{"title":"Severity","value":"critical"}]}]}}`, mattermostChatID)
			w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if call.sent.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", call.sent.Message, tt.wantMessage)
			}
		})
	}
}

func TestMattermost_IconMarksEachAttachment(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, &MattermostConfig{ErrorSeverities: DefaultMattermostErrorSeverities, WarningSeverities: DefaultMattermostWarningSeverities, Icons: DefaultMattermostIcons}, &call)

	payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[
		{"title":"Firing","fields":[{"title":"Severity","value":"critical"}]},
		{"title":"Resolved","fields":[{"title":"Status","value":"resolved"}]}
	]}}`, mattermostChatID)
	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", strings.NewReader(payload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	want := "\U0001F534 Firing\nSeverity: critical\n\n\U0001F7E2 Resolved\nStatus: resolved"
	if call.sent.Message != want {
		t.Errorf("message = %q, want %q", call.sent.Message, want)
	}
}

func TestMattermost_IconSurvivesTheResolveUpdate(t *testing.T) {
	var call mattermostCall
	srv := mattermostServer(t, &MattermostConfig{ErrorSeverities: DefaultMattermostErrorSeverities, WarningSeverities: DefaultMattermostWarningSeverities, Icons: DefaultMattermostIcons}, &call)

	payload := fmt.Sprintf(`{"channel_id":%q,"props":{"attachments":[{"title":"RESOLVED: Disk almost full","text":"The alert has been resolved.","fields":[{"title":"Status","value":"resolved"}]}]}}`, mattermostChatID)
	w := doRequest(srv, "PUT", "/api/v1/mattermost/api/v4/posts/sync-42", strings.NewReader(payload), webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	want := "\U0001F7E2 RESOLVED: Disk almost full\nThe alert has been resolved.\nStatus: resolved"
	if call.edited.Message != want {
		t.Errorf("edited message = %q, want %q", call.edited.Message, want)
	}
}
