# GitLab notifications (universal endpoint)

The `/api/v1/gitlab` endpoint accepts any GitLab group/project webhook. Every
accepted `X-Gitlab-Token` selects one independent entry in
`server.gitlab.senders`; that sender owns its chat scope, event filters,
templates, error events, and routes.

## Files

- `config.yaml` — minimal single-sender configuration.
- `config-routing.yaml` — two isolated teams with explicit chat scopes,
  sender-specific filters/templates/error events, ordered routes, and explicit
  catch-all routes.
- `webhook-merge-request-open.json` — MR opened (`merge_request.open`).
- `webhook-merge-request-merge.json` — MR merged (`merge_request.merge`).
- `webhook-note.json` — comment on an MR (`note.MergeRequest`).
- `webhook-pipeline-failed.json` — failed pipeline (`pipeline.failed`).
- `webhook-push.json` — branch push (`push`).
- `webhook-issue-open.json` — issue opened (`issue.open`).

## Try the minimal example

The example is self-contained; provide the bot, chat, and webhook credentials,
then run:

```bash
export BOT_HOST=express.company.ru \
  BOT_ID=00000000-0000-0000-0000-000000000001 \
  BOT_SECRET=replace-me \
  DEV_CHAT_ID=00000000-0000-0000-0000-000000000002 \
  GITLAB_WEBHOOK_TOKEN=my-secret-token
express-botx serve --config config.yaml &

curl -X POST "http://localhost:8080/api/v1/gitlab" \
  -H "X-Gitlab-Token: my-secret-token" \
  -H "Content-Type: application/json" \
  --data @webhook-merge-request-open.json
```

## Try the routing example

`config-routing.yaml` uses separate tokens for backend and frontend. A sender
can deliver only to its own explicit `chats` scope. Routes select a subset of
that scope; the final `match: {}` rule is the explicit fallback. Without a
matching route, express-botx returns `200` with `ignored: true` and
`reason: "no route matched"`.

```bash
export BOT_HOST=express.company.ru BOT_ID=... BOT_SECRET=... \
  BACKEND_CHAT_ID=... BACKEND_MRS_CHAT_ID=... BACKEND_ONCALL_CHAT_ID=... \
  FRONTEND_CHAT_ID=... FRONTEND_ONCALL_CHAT_ID=... \
  BACKEND_GITLAB_TOKEN=backend-token FRONTEND_GITLAB_TOKEN=frontend-token

express-botx serve --config config-routing.yaml &

# Backend MR: selected by the backend sender's routes.
curl -X POST "http://localhost:8080/api/v1/gitlab" \
  -H "X-Gitlab-Token: backend-token" \
  -H "Content-Type: application/json" \
  --data @webhook-merge-request-open.json

# Query selection bypasses routes but remains inside the authenticated scope.
curl -X POST "http://localhost:8080/api/v1/gitlab?chat_id=frontend-oncall" \
  -H "X-Gitlab-Token: frontend-token" \
  -H "Content-Type: application/json" \
  --data @webhook-pipeline-failed.json

# A backend token cannot target a frontend chat: 403, with nothing delivered.
curl -X POST "http://localhost:8080/api/v1/gitlab?chat_id=frontend" \
  -H "X-Gitlab-Token: backend-token" \
  -H "Content-Type: application/json" \
  --data @webhook-push.json
```

An explicitly empty `?chat_id=` returns `400`. Aliases and their canonical UUIDs
are equivalent for scope checks. `?bot=` is always ignored; in multi-bot mode,
use chat aliases that have a `bot` binding.

## GitLab webhook setup

In a group or project, open **Settings → Webhooks → Add new webhook**:

- **URL:** `http://express-botx:8080/api/v1/gitlab` (optionally with an
  in-scope `?chat_id=<alias>`).
- **Secret token:** the matching `server.gitlab.senders[].secret` value.
- **Triggers:** any desired events; the selected sender's `events.only` and
  `events.exclude` apply additional filtering in express-botx.

See [the GitLab integration documentation](../../docs/integrations.md#gitlab-универсальный-приёмник-событий)
for the complete sender, routing, template, and response semantics.
