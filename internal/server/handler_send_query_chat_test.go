package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"testing"
)

// /send takes chat_id from the query when the body has none, for callers that
// can only send a fixed {"text": ...} body. Body and query must agree when both
// are set, and the query chat goes through the same key scope as the body one.

// sendBody builds a /send body in the given encoding; chatID and bot are
// omitted when empty.
func sendBody(t *testing.T, multi bool, chatID, bot string) (io.Reader, string) {
	t.Helper()
	if !multi {
		fields := map[string]string{"text": "hi"}
		if chatID != "" {
			fields["chat_id"] = chatID
		}
		if bot != "" {
			fields["bot"] = bot
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(raw), "application/json"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fields := [][2]string{{"chat_id", chatID}, {"bot", bot}, {"text", "hi"}}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func TestSend_QueryChatID(t *testing.T) {
	open := ResolvedKey{Name: "any-app", Key: "open"}
	scoped := ResolvedKey{Name: "narrow-app", Key: "narrow", Chats: []string{ownUUID}}

	tests := []struct {
		name          string
		key           string
		query         string
		bodyChat      string
		wantCode      int
		wantDelivered []string
	}{
		{"body chat, no query", "open", "", "own-chat", 200, []string{ownUUID}},
		{"no chat anywhere", "open", "", "", 400, nil},
		{"query alias, no body chat", "open", "?chat_id=own-chat", "", 200, []string{ownUUID}},
		{"query uuid, no body chat", "open", "?chat_id=" + otherUUID, "", 200, []string{otherUUID}},
		{"body and query match", "open", "?chat_id=own-chat", "own-chat", 200, []string{ownUUID}},
		{"body and query list the same chats in another order", "open", "?chat_id=other-chat,own-chat", "own-chat,other-chat", 200, []string{ownUUID, otherUUID}},
		{"body and query differ", "open", "?chat_id=other-chat", "own-chat", 400, nil},
		{"body alias and query uuid are compared literally", "open", "?chat_id=" + ownUUID, "own-chat", 400, nil},
		{"empty query chat_id", "open", "?chat_id=", "", 400, nil},
		{"query chat_id of separators only", "open", "?chat_id=,,", "", 400, nil},
		{"empty query chat_id with body chat", "open", "?chat_id=", "own-chat", 400, nil},
		{"query list fans out", "open", "?chat_id=own-chat," + otherUUID, "", 200, []string{ownUUID, otherUUID}},
		{"repeated query chat_id uses the first", "open", "?chat_id=own-chat&chat_id=other-chat", "", 200, []string{ownUUID}},
		{"repeated query chat_id with an empty first", "open", "?chat_id=&chat_id=own-chat", "", 400, nil},
		{"scoped key, own chat in query", "narrow", "?chat_id=own-chat", "", 200, []string{ownUUID}},
		{"scoped key, foreign alias in query", "narrow", "?chat_id=other-chat", "", 403, nil},
		{"scoped key, foreign uuid in query", "narrow", "?chat_id=" + otherUUID, "", 403, nil},
		{"scoped key, foreign chat in query list", "narrow", "?chat_id=own-chat,other-chat", "", 403, nil},
	}

	for _, enc := range []string{"json", "multipart"} {
		for _, tc := range tests {
			t.Run(enc+"/"+tc.name, func(t *testing.T) {
				var delivered []string
				sendFn := func(_ context.Context, p *SendPayload) (string, error) {
					delivered = append(delivered, p.ChatID)
					return "sync-id", nil
				}
				srv := newScopeServer([]ResolvedKey{open, scoped})
				srv.send = sendFn

				body, ct := sendBody(t, enc == "multipart", tc.bodyChat, "")
				w := doRequest(srv, "POST", "/api/v1/send"+tc.query, body, map[string]string{
					"Content-Type":  ct,
					"Authorization": "Bearer " + tc.key,
				})
				if w.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantCode, w.Body.String())
				}
				if fmt.Sprint(delivered) != fmt.Sprint(tc.wantDelivered) {
					t.Errorf("delivered to %v, want %v", delivered, tc.wantDelivered)
				}
			})
		}
	}
}

func TestSendAsync_QueryChatID(t *testing.T) {
	scoped := ResolvedKey{Name: "narrow-app", Key: "narrow", Chats: []string{ownUUID}}

	tests := []struct {
		name         string
		mode         string
		query        string
		bodyChat     string
		bot          string
		wantCode     int
		wantEnqueued []string
	}{
		{"query alias, fixed text body", "mixed", "?chat_id=own-chat", "", "", 202, []string{"own-chat"}},
		{"query uuid without bot", "mixed", "?chat_id=" + ownUUID, "", "", 400, nil},
		{"query uuid with bot", "mixed", "?chat_id=" + ownUUID, "", "b", 202, []string{ownUUID}},
		{"body and query match", "mixed", "?chat_id=" + ownUUID, ownUUID, "b", 202, []string{ownUUID}},
		{"body and query differ", "mixed", "?chat_id=" + otherUUID, ownUUID, "b", 400, nil},
		{"empty query chat_id", "mixed", "?chat_id=", "", "b", 400, nil},
		{"foreign uuid in query", "mixed", "?chat_id=" + otherUUID, "", "b", 403, nil},
		{"foreign alias in query", "mixed", "?chat_id=other-chat", "", "", 403, nil},
		{"foreign uuid in query list", "mixed", "?chat_id=own-chat," + otherUUID, "", "b", 403, nil},
		// Aliases are authorized inside the pipeline, so a list mixing an
		// allowed and a foreign alias is partially enqueued, as it is when the
		// list comes from the body.
		{"foreign alias in query list", "mixed", "?chat_id=own-chat,other-chat", "", "", 202, []string{"own-chat"}},
		{"direct mode rejects the list before any enqueue", "direct", "?chat_id=" + ownUUID + ",own-chat", "", "", 400, nil},
	}

	for _, enc := range []string{"json", "multipart"} {
		for _, tc := range tests {
			t.Run(enc+"/"+tc.name, func(t *testing.T) {
				var enqueued []string
				aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
				// Stands in for the enqueue pipeline, which resolves aliases and
				// applies the scope to the final address.
				sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
					chatID := p.ChatID
					if id, ok := aliases[chatID]; ok {
						chatID = id
					}
					if !ChatAllowed(ctx, chatID) {
						return "", ErrChatNotAllowed
					}
					enqueued = append(enqueued, p.ChatID)
					return "req-id", nil
				}
				passthrough := func(chatID string) (ChatResolveResult, error) {
					return ChatResolveResult{ChatID: chatID}, nil
				}
				cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped}, AsyncMode: true, DefaultRoutingMode: tc.mode}
				srv := New(cfg, sendFn, passthrough)

				body, ct := sendBody(t, enc == "multipart", tc.bodyChat, tc.bot)
				w := doRequest(srv, "POST", "/api/v1/send"+tc.query, body, map[string]string{
					"Content-Type":  ct,
					"Authorization": "Bearer narrow",
				})
				if w.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantCode, w.Body.String())
				}
				if fmt.Sprint(enqueued) != fmt.Sprint(tc.wantEnqueued) {
					t.Errorf("enqueued %v, want %v", enqueued, tc.wantEnqueued)
				}
			})
		}
	}
}
