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

var DefaultMattermostErrorSeverities = []string{"critical", "high"}

var DefaultMattermostWarningSeverities = []string{"warning", "warn"}

var DefaultMattermostIcons = map[string]string{
	MattermostStateResolved:     "\U0001F7E2",
	MattermostStateAcknowledged: "\U0001F7E1",
	MattermostStateError:        "\U0001F534",
	MattermostStateWarning:      "\U0001F7E0",
	MattermostStateDefault:      "\U0001F535",
}

const (
	MattermostStateResolved     = "resolved"
	MattermostStateAcknowledged = "acknowledged"
	MattermostStateError        = "error"
	MattermostStateWarning      = "warning"
	MattermostStateDefault      = "default"
)

type MattermostConfig struct {
	DefaultChatID     string
	ErrorSeverities   []string
	WarningSeverities []string
	Icons             map[string]string
	FallbackChatID    string
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
		vlog.Info("mattermost: upstream accepted the post for chat %q without a sync_id [key: %s] -> 502 (%dms)", req.target, keyName, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream accepted the message without a sync_id, so it could never be updated")
		return
	}
	vlog.V1("mattermost: post %s submitted to chat %q [key: %s] (%dms)", syncID, req.target, keyName, elapsed.Milliseconds())

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
		Status:  s.resolveMattermostStatus(req.post),
	})
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if err != nil {
		vlog.V1("mattermost: edit of post %s failed [key: %s] -> 502 (%dms): %s", postID, keyName, elapsed.Milliseconds(), boundedCause(err))
		writeError(w, http.StatusBadGateway, s.deliveryError(r.Context(), "editing", err))
		return
	}
	vlog.V1("mattermost: edit of post %s accepted for chat %q [key: %s] (%dms)", postID, req.target, keyName, elapsed.Milliseconds())

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

	message := renderMattermostPost(post, s.mmCfg)
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

func renderMattermostPost(post MattermostPost, cfg *MattermostConfig) string {
	var blocks []string

	if msg := strings.TrimSpace(post.Message); msg != "" {
		blocks = append(blocks, msg)
	}

	for _, att := range post.Props.Attachments {
		var lines []string
		add := func(s string) {
			if s != "" {
				lines = append(lines, s)
			}
		}

		add(strings.TrimSpace(att.Title))
		add(strings.TrimSpace(att.Text))
		for _, f := range att.Fields {
			value := mattermostFieldValue(f.Value)
			if value == "" {
				continue
			}
			if title := strings.TrimSpace(f.Title); title != "" {
				value = title + ": " + value
			}
			add(value)
		}
		add(strings.TrimSpace(att.TitleLink))

		if len(lines) == 0 {
			continue
		}
		if icon := cfg.icon(att); icon != "" {
			lines[0] = icon + " " + lines[0]
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
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

func mattermostFields(att MattermostAttachment) map[string]string {
	out := make(map[string]string, len(att.Fields))
	for _, f := range att.Fields {
		title := strings.ToLower(strings.TrimSpace(f.Title))
		if title == "" {
			continue
		}
		out[title] = strings.ToLower(strings.TrimSpace(mattermostFieldValue(f.Value)))
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.ToLower(strings.TrimSpace(item)) == want {
			return true
		}
	}
	return false
}

func (c *MattermostConfig) state(att MattermostAttachment) string {
	fields := mattermostFields(att)
	status, severity := fields["status"], fields["severity"]

	switch status {
	case MattermostStateResolved:
		return MattermostStateResolved
	case MattermostStateAcknowledged:
		return MattermostStateAcknowledged
	}

	if severity == "" || severity == "-" {
		if status == "" {
			vlog.V1("mattermost: attachment carries neither a Status nor a Severity field, classified as %q", MattermostStateDefault)
		}
		return MattermostStateDefault
	}
	if containsFold(c.ErrorSeverities, severity) {
		return MattermostStateError
	}
	if containsFold(c.WarningSeverities, severity) {
		return MattermostStateWarning
	}
	return MattermostStateDefault
}

func (c *MattermostConfig) postState(post MattermostPost) string {
	if len(post.Props.Attachments) == 0 {
		return MattermostStateDefault
	}
	return c.state(post.Props.Attachments[0])
}

func (c *MattermostConfig) icon(att MattermostAttachment) string {
	if len(c.Icons) == 0 {
		return ""
	}
	return c.Icons[c.state(att)]
}

func (s *Server) resolveMattermostStatus(post MattermostPost) string {
	if s.mmCfg.postState(post) == MattermostStateError {
		return "error"
	}
	return "ok"
}
