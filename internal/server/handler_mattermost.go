package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	vlog "github.com/lavr/express-botx/internal/log"
)

var DefaultMattermostErrorColors = []string{"#d9534f"}

type MattermostConfig struct {
	DefaultChatID  string
	ErrorColors    []string
	FallbackChatID string
}

type MattermostPost struct {
	ID        string          `json:"id"`
	ChannelID string          `json:"channel_id"`
	Message   string          `json:"message"`
	Props     MattermostProps `json:"props"`
}

type MattermostProps struct {
	Attachments []MattermostAttachment `json:"attachments"`
}

type MattermostAttachment struct {
	Fallback  string            `json:"fallback"`
	Color     string            `json:"color"`
	Title     string            `json:"title"`
	TitleLink string            `json:"title_link"`
	Text      string            `json:"text"`
	Fields    []MattermostField `json:"fields"`
}

type MattermostField struct {
	Title string `json:"title"`
	Value any    `json:"value"`
	Short bool   `json:"short"`
}

type mattermostPostResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
}

type mattermostRequest struct {
	post    MattermostPost
	target  string
	chatID  string
	bot     string
	message string
}

func (c *MattermostConfig) singleChat(globalDefault string) string {
	if c.DefaultChatID != "" {
		return c.DefaultChatID
	}
	if globalDefault != "" {
		return globalDefault
	}
	return c.FallbackChatID
}

func (s *Server) handleMattermostCreatePost(w http.ResponseWriter, r *http.Request) {
	req, ok := s.prepareMattermostPost(w, r)
	if !ok {
		return
	}

	start := time.Now()
	syncID, err := s.send(r.Context(), &SendPayload{
		Bot:     req.bot,
		ChatID:  req.chatID,
		Message: req.message,
		Status:  s.resolveMattermostStatus(req.post),
	})
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if err != nil {
		vlog.V1("mattermost: post to chat %q failed [key: %s] -> 502 (%dms): %s", req.target, keyName, elapsed.Milliseconds(), boundedCause(err))
		writeError(w, http.StatusBadGateway, s.deliveryError(r.Context(), "sending", err))
		return
	}
	if syncID == "" {
		vlog.Info("mattermost: chat %q accepted the post but returned no sync_id [key: %s] -> 502 (%dms)", req.target, keyName, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "message delivered without a sync_id, so it could never be updated")
		return
	}
	vlog.V1("mattermost: created post %s in chat %q [key: %s] (%dms)", syncID, req.target, keyName, elapsed.Milliseconds())

	writeMattermostPost(w, syncID, req.post.ChannelID)
}

func (s *Server) handleMattermostUpdatePost(w http.ResponseWriter, r *http.Request) {
	postID := strings.TrimSpace(chi.URLParam(r, "post_id"))
	if postID == "" {
		writeError(w, http.StatusBadRequest, "post_id is required")
		return
	}
	if s.edit == nil {
		writeError(w, http.StatusNotImplemented, "editing messages is not supported in this mode")
		return
	}
	if Scoped(r.Context()) {
		writeError(w, http.StatusForbidden, "editing a post is not available to a chat-scoped key: a post_id does not carry the chat it lives in, so the scope cannot be applied")
		return
	}

	req, ok := s.prepareMattermostPost(w, r)
	if !ok {
		return
	}

	start := time.Now()
	err := s.edit(r.Context(), &EditPayload{
		Bot:     req.bot,
		SyncID:  postID,
		Message: req.message,
	})
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if err != nil {
		vlog.V1("mattermost: edit of post %s failed [key: %s] -> 502 (%dms): %s", postID, keyName, elapsed.Milliseconds(), boundedCause(err))
		writeError(w, http.StatusBadGateway, s.deliveryError(r.Context(), "editing", err))
		return
	}
	vlog.V1("mattermost: updated post %s in chat %q [key: %s] (%dms)", postID, req.target, keyName, elapsed.Milliseconds())

	writeMattermostPost(w, postID, req.post.ChannelID)
}

func (s *Server) prepareMattermostPost(w http.ResponseWriter, r *http.Request) (*mattermostRequest, bool) {
	if s.mmCfg == nil {
		writeError(w, http.StatusInternalServerError, "mattermost not configured")
		return nil, false
	}

	trace := s.newPayloadTrace(r)
	body, err := readRawBody(r, trace, "mattermost")
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body: "+err.Error())
		return nil, false
	}

	var post MattermostPost
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&post); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return nil, false
	}

	target := strings.TrimSpace(post.ChannelID)
	if target == "" {
		target = s.mmCfg.singleChat(s.cfg.DefaultChatAlias)
	}
	if target == "" {
		writeError(w, http.StatusBadRequest, "channel_id is required: send it in the body or set server.mattermost.default_chat_id")
		return nil, false
	}

	message := renderMattermostPost(post)
	if message == "" {
		writeError(w, http.StatusBadRequest, "no content in payload: message and props.attachments are both empty")
		return nil, false
	}
	trace.renderedMessage("mattermost", message)

	if !s.authorizeTargets(w, r, []string{target}, true) {
		return nil, false
	}

	chat, err := s.chats(target)
	if err != nil {
		s.denyChat(w, r, err, "resolving chat")
		return nil, false
	}

	bot, errMsg := s.resolveRequestBot(r.Context(), "", chat.Bot)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return nil, false
	}

	return &mattermostRequest{
		post:    post,
		target:  target,
		chatID:  chat.ChatID,
		bot:     bot,
		message: message,
	}, true
}

func writeMattermostPost(w http.ResponseWriter, id, channelID string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mattermostPostResponse{ID: id, ChannelID: channelID}) //nolint:errcheck
}

func renderMattermostPost(post MattermostPost) string {
	var b strings.Builder
	line := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}

	line(strings.TrimSpace(post.Message))

	for _, att := range post.Props.Attachments {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		line(strings.TrimSpace(att.Title))
		line(strings.TrimSpace(att.Text))
		for _, f := range att.Fields {
			value := mattermostFieldValue(f.Value)
			if value == "" {
				continue
			}
			if title := strings.TrimSpace(f.Title); title != "" {
				value = title + ": " + value
			}
			line(value)
		}
		line(strings.TrimSpace(att.TitleLink))
	}

	return strings.TrimSpace(b.String())
}

func mattermostFieldValue(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		encoded, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		return strings.Trim(string(encoded), `"`)
	}
}

func (s *Server) resolveMattermostStatus(post MattermostPost) string {
	if len(post.Props.Attachments) == 0 {
		return "ok"
	}
	color := strings.ToLower(strings.TrimSpace(post.Props.Attachments[0].Color))
	if color == "" {
		return "ok"
	}
	for _, c := range s.mmCfg.ErrorColors {
		if strings.ToLower(strings.TrimSpace(c)) == color {
			return "error"
		}
	}
	return "ok"
}
