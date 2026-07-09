# GitLab notifications (universal endpoint)

Example configuration and payloads for the `/api/v1/gitlab` webhook endpoint,
which forwards **any** GitLab group/project event to an eXpress chat. The payload
is decoded generically and reduced to an event key (`kind` or `kind.subtype`),
then filtered and rendered by a per-event template registry.

## Files

- `config.yaml` — express-botx config enabling `server.gitlab` with an
  `only`/`exclude` filter, per-event `templates`, and `error_events` (single
  chat via `default_chat_id`).
- `config-routing.yaml` — same endpoint with `server.gitlab.routes`: one event
  fans out to multiple chats by project/event/branch (all-match + `stop`, with a
  `default_chat_id` fallback).
- `config-senders.yaml` — same endpoint with `server.gitlab.senders`: two teams
  with their own `X-Gitlab-Token` values, each isolated to its own chats, plus
  the shared default `secret` (mixed mode).
- `webhook-merge-request-open.json` — MR opened (`merge_request.open`).
- `webhook-merge-request-merge.json` — MR merged (`merge_request.merge`).
- `webhook-note.json` — comment on an MR (`note.MergeRequest`).
- `webhook-pipeline-failed.json` — failed pipeline (`pipeline.failed`, delivered
  with `status=error`).
- `webhook-push.json` — branch push (`push`).
- `webhook-issue-open.json` — issue opened (`issue.open`).

## Try it locally

```bash
export BOT_HOST=express.company.ru BOT_ID=... BOT_SECRET=... \
       DEV_CHAT_ID=... GITLAB_WEBHOOK_TOKEN=my-secret-token

express-botx serve --config config.yaml &

for f in webhook-merge-request-open webhook-note webhook-pipeline-failed \
         webhook-push webhook-issue-open; do
  curl -X POST "http://localhost:8080/api/v1/gitlab" \
    -H "X-Gitlab-Token: my-secret-token" \
    -H "Content-Type: application/json" \
    --data @"$f.json"
  echo
done
```

`merge_request.update` is in `exclude`, so a payload with `"action":"update"`
is answered with `200 OK` and `{"ok":true,"ignored":true,"event":"merge_request.update"}`
without sending a message.

## GitLab webhook setup

In a group or project: **Settings → Webhooks → Add new webhook**

- **URL:** `http://express-botx:8080/api/v1/gitlab` (optionally `?chat_id=<alias>`)
- **Secret token:** same value as `server.gitlab.secret` — or, when using
  `server.gitlab.senders`, your team's own sender token
  (see [Per-team tokens](#per-team-tokens-senders))
- **Triggers:** enable whichever events you want — or all of them, since filtering
  now happens in express-botx via `events.only` / `events.exclude`.

## Event keys and subtypes

The event key is `kind` or `kind.subtype`:

| `object_kind` | subtype from | example key |
|---|---|---|
| `merge_request`, `issue` | `object_attributes.action` | `merge_request.open` |
| `note` | `object_attributes.noteable_type` | `note.MergeRequest` |
| `pipeline` | `object_attributes.status` | `pipeline.failed` |
| `build` (job) | `build_status` | `build.failed` |
| `push`, `tag_push` | — | `push` |

See [docs/integrations.md](../../docs/integrations.md#gitlab-универсальный-приёмник-событий)
for the filter rules, template registry, template variables/helpers, and
`error_events`.

## Routing one event to several chats

`config-routing.yaml` adds `server.gitlab.routes` — an ordered rule list that
fans a single event out to one or more chats by project, event key, and branch
(glob or `/regex/` patterns). All matching rules contribute their chats (unioned
and de-duplicated); a rule with `stop: true` ends the scan; unmatched events fall
back to `default_chat_id`. Delivery is best-effort: the response is always a
`MultiSendResponse` — `200` with `{"ok":true,"results":[…],"errors":[…]}` once at
least one chat is delivered, `502` if they all fail. A comma-separated
`?chat_id=a,b` fans out the same way. See the
[routing section](../../docs/integrations.md#роутинг-событий-по-чатам-routes)
and [Мульти-чат](../../docs/integrations.md#мульти-чат-и-единый-ответ-multisendresponse)
for the full model, response format and chat-selection priority.

## Per-team tokens (senders)

`config-senders.yaml` adds `server.gitlab.senders` — extra incoming
`X-Gitlab-Token` values, each hard-bound to its own chats. A request
authenticated with a sender token is delivered **only** to that sender's chats
(`?chat_id=`, `routes` and `default_chat_id` are ignored), so teams sharing one
endpoint cannot post into each other's chats. The global `events` filter,
templates and `error_events` apply as usual; the default `secret` keeps its
ordinary behaviour and may be omitted when only senders are used. Token values
are `env:`/`vault:` references — the shared YAML never contains plaintext
secrets, and duplicate resolved tokens fail at startup. See the
[senders section](../../docs/integrations.md#изоляция-команд-senders-несколько-токенов)
for details.
