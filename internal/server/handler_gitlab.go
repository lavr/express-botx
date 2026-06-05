package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/lavr/express-botx/internal/botapi"
	vlog "github.com/lavr/express-botx/internal/log"
)

// GitLabConfig holds settings for the GitLab webhook endpoint.
type GitLabConfig struct {
	Token         string
	DefaultChatID string
	Projects      map[string]string
	Events        map[string]bool
	// FallbackChatID is resolved at startup from the config's chats section
	// when there is exactly one chat alias configured. Empty otherwise.
	FallbackChatID string
}

type gitLabProject struct {
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type gitLabUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

type gitLabCommit struct {
	ID        string     `json:"id"`
	Message   string     `json:"message"`
	Timestamp *time.Time `json:"timestamp"`
	URL       string     `json:"url"`
	Author    struct {
		Name string `json:"name"`
	} `json:"author"`
}

type gitLabPushWebhook struct {
	ObjectKind        string         `json:"object_kind"`
	Ref               string         `json:"ref"`
	Before            string         `json:"before"`
	After             string         `json:"after"`
	UserName          string         `json:"user_name"`
	UserUsername      string         `json:"user_username"`
	Project           gitLabProject  `json:"project"`
	Commits           []gitLabCommit `json:"commits"`
	TotalCommitsCount int            `json:"total_commits_count"`
	CheckoutSHA       string         `json:"checkout_sha"`
}

type gitLabMergeRequestWebhook struct {
	ObjectKind       string        `json:"object_kind"`
	User             gitLabUser    `json:"user"`
	Project          gitLabProject `json:"project"`
	ObjectAttributes struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		State        string `json:"state"`
		Action       string `json:"action"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		URL          string `json:"url"`
	} `json:"object_attributes"`
}

type gitLabPipelineWebhook struct {
	ObjectKind       string        `json:"object_kind"`
	User             gitLabUser    `json:"user"`
	Project          gitLabProject `json:"project"`
	ObjectAttributes struct {
		ID       int    `json:"id"`
		Ref      string `json:"ref"`
		Status   string `json:"status"`
		Duration int    `json:"duration"`
		URL      string `json:"url"`
		SHA      string `json:"sha"`
	} `json:"object_attributes"`
}

type gitLabJobWebhook struct {
	ObjectKind  string        `json:"object_kind"`
	Ref         string        `json:"ref"`
	Tag         bool          `json:"tag"`
	BuildID     int           `json:"build_id"`
	BuildName   string        `json:"build_name"`
	BuildStage  string        `json:"build_stage"`
	BuildStatus string        `json:"build_status"`
	BuildURL    string        `json:"build_url"`
	User        gitLabUser    `json:"user"`
	Project     gitLabProject `json:"project"`
}

func (s *Server) handleGitLab(w http.ResponseWriter, r *http.Request) {
	if s.glCfg == nil {
		writeError(w, http.StatusInternalServerError, "gitlab not configured")
		return
	}
	if s.glCfg.Token == "" {
		writeError(w, http.StatusInternalServerError, "gitlab token not configured")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gitlab-Token")), []byte(s.glCfg.Token)) != 1 {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	body := json.RawMessage{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	event := normalizeGitLabEvent(r.Header.Get("X-Gitlab-Event"))
	if event == "" {
		writeError(w, http.StatusBadRequest, "unsupported or missing X-Gitlab-Event")
		return
	}
	if !s.gitLabEventEnabled(event) {
		vlog.V2("gitlab: event %q disabled", event)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(sendResponse{OK: true, Queued: true})
		return
	}

	msg, err := formatGitLabMessage(event, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetChat := s.resolveGitLabTargetChat(r, msg.ProjectPath)
	if targetChat == "" {
		writeError(w, http.StatusBadRequest, "chat_id is required: set gitlab.default_chat_id, configure a single chat alias, map the project, or pass ?chat_id=")
		return
	}
	chatResult, err := s.chats(targetChat)
	if err != nil {
		writeError(w, http.StatusBadRequest, "resolving chat: "+err.Error())
		return
	}

	botName, errMsg := s.resolveRequestBot(r.Context(), r.URL.Query().Get("bot"), chatResult.Bot)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	start := time.Now()
	syncID, err := s.send(r.Context(), &SendPayload{
		Bot:     botName,
		ChatID:  chatResult.ChatID,
		Message: msg.Text,
		Status:  msg.Status,
		Bubble:  gitLabButton(msg.URL),
	})
	elapsed := time.Since(start)

	if err != nil {
		vlog.V1("gitlab: send failed project=%s event=%s -> 502 (%dms)", msg.ProjectPath, event, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	vlog.V1("gitlab: sent project=%s event=%s to %s -> 200 (%dms)", msg.ProjectPath, event, targetChat, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
}

func normalizeGitLabEvent(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "push hook":
		return "push"
	case "merge request hook":
		return "merge_request"
	case "tag push hook":
		return "tag_push"
	case "pipeline hook":
		return "pipeline"
	case "job hook":
		return "job"
	default:
		return ""
	}
}

func (s *Server) gitLabEventEnabled(event string) bool {
	if len(s.glCfg.Events) == 0 {
		return true
	}
	return s.glCfg.Events[event]
}

func (s *Server) resolveGitLabTargetChat(r *http.Request, projectPath string) string {
	if q := r.URL.Query().Get("chat_id"); q != "" {
		return q
	}
	if s.glCfg.Projects != nil {
		if chatID := s.glCfg.Projects[projectPath]; chatID != "" {
			return chatID
		}
	}
	if s.glCfg.DefaultChatID != "" {
		return s.glCfg.DefaultChatID
	}
	if s.cfg.DefaultChatAlias != "" {
		return s.cfg.DefaultChatAlias
	}
	return s.glCfg.FallbackChatID
}

type gitLabMessage struct {
	Text        string
	Status      string
	URL         string
	ProjectPath string
}

func formatGitLabMessage(event string, body []byte) (gitLabMessage, error) {
	switch event {
	case "push":
		return formatGitLabPush(body, false)
	case "tag_push":
		return formatGitLabPush(body, true)
	case "merge_request":
		return formatGitLabMergeRequest(body)
	case "pipeline":
		return formatGitLabPipeline(body)
	case "job":
		return formatGitLabJob(body)
	default:
		return gitLabMessage{}, fmt.Errorf("unsupported GitLab event %q", event)
	}
}

func formatGitLabPush(body []byte, tagPush bool) (gitLabMessage, error) {
	var p gitLabPushWebhook
	if err := json.Unmarshal(body, &p); err != nil {
		return gitLabMessage{}, fmt.Errorf("invalid GitLab push payload: %w", err)
	}
	projectPath := gitLabProjectPath(p.Project)
	ref := gitLabRefName(p.Ref)
	actor := firstNonEmpty(p.UserName, p.UserUsername, "unknown user")
	status := "ok"
	action := "pushed"
	if isZeroGitSHA(p.After) {
		action = "deleted"
	}
	if tagPush {
		text := fmt.Sprintf("GitLab tag %s in %s: %s by %s", ref, projectPath, action, actor)
		return gitLabMessage{Text: text, Status: status, URL: p.Project.WebURL, ProjectPath: projectPath}, nil
	}

	count := p.TotalCommitsCount
	if count == 0 {
		count = len(p.Commits)
	}
	text := fmt.Sprintf("GitLab push to %s/%s: %d commit(s) %s by %s", projectPath, ref, count, action, actor)
	if len(p.Commits) > 0 {
		text += "\n" + gitLabCommitSummary(p.Commits)
	}
	url := p.Project.WebURL
	if p.After != "" && !isZeroGitSHA(p.After) {
		url = p.Project.WebURL + "/-/commit/" + p.After
	}
	return gitLabMessage{Text: text, Status: status, URL: url, ProjectPath: projectPath}, nil
}

func formatGitLabMergeRequest(body []byte) (gitLabMessage, error) {
	var mr gitLabMergeRequestWebhook
	if err := json.Unmarshal(body, &mr); err != nil {
		return gitLabMessage{}, fmt.Errorf("invalid GitLab merge request payload: %w", err)
	}
	projectPath := gitLabProjectPath(mr.Project)
	a := mr.ObjectAttributes
	actor := firstNonEmpty(mr.User.Name, mr.User.Username, "unknown user")
	action := firstNonEmpty(a.Action, a.State, "updated")
	text := fmt.Sprintf("GitLab MR !%d %s in %s: %s\n%s -> %s by %s", a.IID, action, projectPath, a.Title, a.SourceBranch, a.TargetBranch, actor)
	return gitLabMessage{Text: text, Status: "ok", URL: a.URL, ProjectPath: projectPath}, nil
}

func formatGitLabPipeline(body []byte) (gitLabMessage, error) {
	var p gitLabPipelineWebhook
	if err := json.Unmarshal(body, &p); err != nil {
		return gitLabMessage{}, fmt.Errorf("invalid GitLab pipeline payload: %w", err)
	}
	projectPath := gitLabProjectPath(p.Project)
	a := p.ObjectAttributes
	actor := firstNonEmpty(p.User.Name, p.User.Username, "unknown user")
	text := fmt.Sprintf("GitLab pipeline #%d %s in %s/%s by %s", a.ID, a.Status, projectPath, a.Ref, actor)
	if a.Duration > 0 {
		text += fmt.Sprintf(" (%s)", (time.Duration(a.Duration) * time.Second).String())
	}
	return gitLabMessage{Text: text, Status: gitLabStatus(a.Status), URL: a.URL, ProjectPath: projectPath}, nil
}

func formatGitLabJob(body []byte) (gitLabMessage, error) {
	var j gitLabJobWebhook
	if err := json.Unmarshal(body, &j); err != nil {
		return gitLabMessage{}, fmt.Errorf("invalid GitLab job payload: %w", err)
	}
	projectPath := gitLabProjectPath(j.Project)
	actor := firstNonEmpty(j.User.Name, j.User.Username, "unknown user")
	text := fmt.Sprintf("GitLab job %s/%s #%d %s in %s/%s by %s", j.BuildStage, j.BuildName, j.BuildID, j.BuildStatus, projectPath, j.Ref, actor)
	return gitLabMessage{Text: text, Status: gitLabStatus(j.BuildStatus), URL: j.BuildURL, ProjectPath: projectPath}, nil
}

func gitLabProjectPath(project gitLabProject) string {
	return firstNonEmpty(project.PathWithNamespace, project.Name, "unknown/project")
}

func gitLabRefName(ref string) string {
	if ref == "" {
		return "unknown"
	}
	return path.Base(ref)
}

func gitLabCommitSummary(commits []gitLabCommit) string {
	limit := len(commits)
	if limit > 3 {
		limit = 3
	}
	lines := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		sha := commits[i].ID
		if len(sha) > 8 {
			sha = sha[:8]
		}
		message := strings.TrimSpace(strings.SplitN(commits[i].Message, "\n", 2)[0])
		author := commits[i].Author.Name
		if author != "" {
			lines = append(lines, fmt.Sprintf("- %s %s (%s)", sha, message, author))
		} else {
			lines = append(lines, fmt.Sprintf("- %s %s", sha, message))
		}
	}
	if len(commits) > limit {
		lines = append(lines, fmt.Sprintf("- ... and %d more", len(commits)-limit))
	}
	return strings.Join(lines, "\n")
}

func gitLabStatus(status string) string {
	switch strings.ToLower(status) {
	case "failed", "canceled", "cancelled", "skipped":
		return "error"
	default:
		return "ok"
	}
}

func gitLabButton(url string) botapi.ButtonMarkup {
	if url == "" {
		return nil
	}
	silent := true
	return botapi.ButtonMarkup{{
		{
			Label: "Open GitLab",
			Opts: &botapi.ButtonOpts{
				Silent:  &silent,
				Align:   "center",
				Handler: "client",
				Link:    url,
			},
		},
	}}
}

func isZeroGitSHA(s string) bool {
	return s != "" && strings.Trim(s, "0") == ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
