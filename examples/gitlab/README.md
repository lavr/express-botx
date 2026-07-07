# GitLab merge request notifications

Example configuration and payloads for the `/api/v1/gitlab` webhook endpoint,
which forwards GitLab merge request events to an eXpress chat.

## Files

- `config.yaml` — express-botx config enabling the `server.gitlab` endpoint.
- `webhook-merge-request-open.json` — MR opened event.
- `webhook-merge-request-merge.json` — MR merged event.
- `webhook-note.json` — comment on an MR.

## Try it locally

```bash
export BOT_HOST=express.company.ru BOT_ID=... BOT_SECRET=... \
       DEV_CHAT_ID=... GITLAB_WEBHOOK_TOKEN=my-secret-token

express-botx serve --config config.yaml &

curl -X POST "http://localhost:8080/api/v1/gitlab" \
  -H "X-Gitlab-Token: my-secret-token" \
  -H "Content-Type: application/json" \
  --data @webhook-merge-request-open.json
```

## GitLab webhook setup

In a group or project: **Settings → Webhooks → Add new webhook**

- **URL:** `http://express-botx:8080/api/v1/gitlab` (optionally `?chat_id=<alias>`)
- **Secret token:** same value as `server.gitlab.secret`
- **Triggers:** Merge request events, Comments

Events other than MR open/merge and MR comments (update/close, notes on commits,
system notes) are answered with `200 OK` and `"ignored": true` — no message sent.

See [docs/integrations.md](../../docs/integrations.md#gitlab-merge-requests) for details.
