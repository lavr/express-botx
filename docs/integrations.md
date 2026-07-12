# Интеграции

Подключение express-botx к системам мониторинга.

## Мульти-чат и единый ответ (`MultiSendResponse`)

Все точки отправки (`/send`, `/alertmanager`, `/grafana`, `/gitlab` и CLI)
поддерживают **несколько чатов через запятую** в `chat_id` и возвращают **единый
формат ответа**.

### `chat_id` через запятую (фан-аут)

`chat_id=a,b,c` (в теле `/send` или в query `?chat_id=a,b,c` у вебхуков)
рассылает одно сообщение во **все** перечисленные чаты (fan-out). Дубликаты
схлопываются, порядок сохраняется, чат и бот резолвятся для каждого таргета
отдельно. Доставка **best-effort**: успех в один чат не блокируется падением
другого.

### Единый ответ `MultiSendResponse` (ломающее изменение)

> **⚠️ Ломающее изменение формата ответа.** Раньше `/send`/`/alertmanager`/
> `/grafana` отвечали `{"ok":true,"sync_id":"..."}`, а `/gitlab` — то одиночной,
> то fan-out-формой. Теперь **все** эндпоинты, даже для одного чата, отвечают
> единым конвертом. Клиентам, которым нужен старый контракт, следует остаться на
> предыдущей версии.

```json
{"ok": true,
 "results": [{"chat":"a","sync_id":"..."},
             {"chat":"b","request_id":"...","queued":true}],
 "errors":  [{"chat":"c","error":"resolving chat: ..."}]}
```

- Пер-чатовый успех: `sync_id` (sync-отправка) либо `request_id`+`queued:true`
  (async/enqueue).
- Пер-чатовые сбои доставки (резолв чата/бота, ошибка upstream) — в `errors[]`.
- **Коды ответа:** `200` — sync, доставлено ≥1 чата; `202` — async (enqueue);
  `502` — во все чаты не удалось.
- **Request-level ошибки** (битый JSON, пустой `chat_id`, невалидный `status`,
  неподдерживаемый media-type) сохраняют прежнюю форму `{"ok":false,"error":"..."}`
  с кодами `400`/`415` и в `errors[]` **не** попадают.

### Async: expand в N сообщений

В async-режиме (`serve --enqueue`) `chat_id=a,b,c` раскрывается в **N отдельных
сообщений в очереди** (по одному на чат) — так каждый чат ретраится/подтверждается
независимо, без дублей; worker не меняется.

### Переиспользуемый паттерн (для разработчиков)

Мульти-чат — единый механизм на весь проект, а не копипаста по хендлерам. Любая
новая точка отправки должна переиспользовать примитивы из
[`internal/server/multisend.go`](../internal/server/multisend.go), а не
реализовывать fan-out заново:

- `parseChatIDs(raw)` — разбор `chat_id` через запятую (trim, dedup, порядок
  сохранён, пустые отброшены).
- `fanout(ctx, targets, deliver)` — best-effort обход таргетов: собирает
  `[]SendResult` и `[]SendError`, порядок сохранён.
- `(*Server).fanoutSend(...)` — готовый `deliver` с резолвом чата+бота для
  простых поверхностей без mentions (`/alertmanager`, `/grafana`).
- `MultiSendResponse` / `writeMultiSend(w, results, errs, successStatus)` —
  единый конверт ответа и коды `200`/`202`/`502`.

Request-level ошибки (битый вход, media-type) остаются вне `errors[]` в форме
`{"ok":false,"error":"..."}`. Кастомный `deliver` нужен только там, где per-target
логика отличается (например, per-bot mentions в `/send` sync).

---

## Alertmanager

### Настройка express-botx

Endpoint `/api/v1/alertmanager` включён по умолчанию — никакой дополнительной настройки не требуется. Если в конфиге определён один чат, он используется автоматически. Секция `alertmanager` нужна только для кастомизации:

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  api_keys:
    - name: alertmanager
      key: env:ALERTMANAGER_API_KEY
  alertmanager:                       # опционально — endpoint работает и без этой секции
    default_chat_id: alerts           # чат по умолчанию для алертов
    error_severities:                 # при каких severity ставить статус "error"
      - critical
      - warning
```

### Настройка Alertmanager

Добавьте receiver в `alertmanager.yml`:

```yaml
receivers:
  - name: express
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager
        send_resolved: true
        http_config:
          bearer_token: "<api-key>"

route:
  receiver: express
  # Или для конкретных алертов:
  routes:
    - match:
        severity: critical
      receiver: express
```

### Несколько чатов

Один алерт можно разослать сразу в несколько чатов через запятую в `?chat_id=`
(fan-out, единый ответ — см. [Мульти-чат](#мульти-чат-и-единый-ответ-multisendresponse)):
`...?chat_id=infra-alerts,app-alerts`.

Если же разные алерты должны идти в разные чаты, используйте разные receiver'ы с query-параметром `chat_id`:

```yaml
receivers:
  - name: express-infra
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager?chat_id=infra-alerts
        send_resolved: true
        http_config:
          bearer_token: "<api-key>"

  - name: express-app
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager?chat_id=app-alerts
        send_resolved: true
        http_config:
          bearer_token: "<api-key>"

route:
  routes:
    - match:
        team: infra
      receiver: express-infra
    - match:
        team: app
      receiver: express-app
```

### Проверка вручную

```bash
curl -X POST http://localhost:8080/api/v1/alertmanager \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "firing",
    "alerts": [
      {
        "status": "firing",
        "labels": {
          "alertname": "HighCPU",
          "severity": "critical",
          "instance": "server1:9090"
        },
        "annotations": {
          "summary": "CPU usage is above 90%"
        },
        "startsAt": "2026-01-01T00:00:00Z",
        "endsAt": "0001-01-01T00:00:00Z"
      }
    ]
  }'
```

---

## Grafana

### Настройка express-botx

Endpoint `/api/v1/grafana` включён по умолчанию — никакой дополнительной настройки не требуется. Если в конфиге определён один чат, он используется автоматически. Секция `grafana` нужна только для кастомизации:

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  api_keys:
    - name: grafana
      key: env:GRAFANA_API_KEY
  grafana:                              # опционально — endpoint работает и без этой секции
    default_chat_id: alerts             # чат по умолчанию
    error_states:                       # при каких состояниях ставить статус "error"
      - alerting
```

### Настройка Grafana

1. Перейдите в **Alerting → Contact points → Add contact point**
2. Выберите тип **Webhook**
3. Заполните:
   - **URL:** `http://express-botx:8080/api/v1/grafana`
   - **HTTP Method:** POST
   - **Authorization Header:** `Bearer <api-key>`
4. Сохраните и привяжите к notification policy

### Несколько чатов

Аналогично Alertmanager — либо один contact point с фан-аутом через запятую
(`?chat_id=infra-alerts,app-alerts`), либо несколько contact point'ов с `?chat_id=`:

- `http://express-botx:8080/api/v1/grafana?chat_id=infra-alerts`
- `http://express-botx:8080/api/v1/grafana?chat_id=app-alerts`

### Проверка вручную

```bash
curl -X POST http://localhost:8080/api/v1/grafana \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "firing",
    "alerts": [
      {
        "status": "firing",
        "labels": {
          "alertname": "DiskFull"
        },
        "annotations": {
          "summary": "Disk usage above 90%"
        },
        "startsAt": "2026-01-01T00:00:00Z",
        "dashboardURL": "https://grafana.company.ru/d/abc123"
      }
    ]
  }'
```

---

## GitLab (универсальный приёмник событий)

Endpoint `/api/v1/gitlab` принимает **любые** group/project-вебхуки GitLab
(`merge_request`, `note`, `push`, `tag_push`, `pipeline`, `build`/`job`, `issue`,
`release`, `wiki_page`, `deployment`, … и любые будущие) и отправляет их в чат
eXpress. Новый тип события не требует правок в Go: полезная нагрузка разбирается
generic-декодером, а событие сводится к **event-ключу** и рендерится шаблоном.

Аутентификация — по заголовку `X-Gitlab-Token` (GitLab не умеет ставить
`Authorization`/`X-API-Key`), поэтому эндпоинт не использует обычные `api_keys`.
Секция `server.gitlab` содержит только `senders`: каждый токен выбирает свой
полностью независимый набор чатов, фильтров, шаблонов, error-событий и
маршрутов.

### Event-ключ и деривация субтипа

Event-ключ — это строка `kind` или `kind.subtype`, где `kind` = `object_kind`,
а субтип зависит от типа события:

| `object_kind` | Субтип берётся из | Пример ключа |
|---|---|---|
| `merge_request`, `issue` | `object_attributes.action` | `merge_request.open`, `issue.close` |
| `note` | `object_attributes.noteable_type` | `note.MergeRequest`, `note.Commit` |
| `pipeline` | `object_attributes.status` | `pipeline.failed`, `pipeline.success` |
| `build` (job) | `build_status` (плоское поле) | `build.failed`, `build.success` |
| `push`, `tag_push` | — (только `kind`) | `push`, `tag_push` |
| прочее | `object_attributes.action`, затем `.status` | `deployment.success` |

### Фильтр событий: only / exclude

Через `server.gitlab.senders[].events` можно ограничить события конкретного
sender'а.
Каждая запись фильтра матчит: полный event-ключ (`kind.subtype`), голый `kind`
(все субтипы этого типа) или wildcard `kind.*` (то же, что голый `kind`).

- `only` пустой → проходят все события; `only` непустой → событие должно матчиться.
- `exclude` вычитает и **всегда выигрывает** над `only`.
- Событие, не прошедшее фильтр → `200 OK` с телом
  `{"ok":true,"ignored":true,"event":"<key>","reason":"event filtered"}` и без
  отправки сообщения (пустой отрендеренный шаблон даёт то же с
  `reason:"empty message"`).

```yaml
events:
  only:    ["merge_request.*", "pipeline.failed", "push"]
  exclude: ["merge_request.update"]
```

### Шаблоны событий

Сообщение рендерится реестром шаблонов: сначала берётся точный ключ
`kind.subtype`, затем голый `kind`, затем генерик `default` — поэтому любое
событие всегда отрендерится. Встроенные дефолты покрывают частые события
(`merge_request.open/merge/close`, `note.MergeRequest`, `push`, `tag_push`,
`pipeline`, `issue`) и `default`; любой ключ переопределяется в конфиге через
`templates` (inline) или `template_files` (путь к файлу). Один и тот же ключ
нельзя задать в обоих сразу. Ключи `kind` и `kind.*` эквивалентны (оба — catch-all
для всех субтипов) и занимают один слот реестра: задать оба сразу — ошибка
валидации; конкретный `kind.subtype` при этом можно задавать рядом с `kind`.

Доступные переменные шаблона (view-модель):

| Переменная | Значение |
|---|---|
| `.Kind` | `object_kind` |
| `.Action` | `object_attributes.action` или производный субтип |
| `.EventKey` | `kind` или `kind.subtype` |
| `.Project` | `project.name` (fallback `project.path_with_namespace`) |
| `.User` | `user.name` / `user.username` (fallback `user_name` для push) |
| `.Title` | `object_attributes.title` |
| `.URL` | `object_attributes.url` (fallback `project.web_url`) |
| `.Raw` | весь декодированный payload (`map`) |

Хелперы: `get` — доступ по вложенному пути в payload
(`{{ get .Raw "object_attributes.note" }}`, `nil` если нет; эквивалент
`{{ .Get "object_attributes.note" }}`); `default` — значение по умолчанию
(`{{ default "n/a" .Title }}`).

```yaml
templates:
  "merge_request.open": "🆕 {{.Title}} — {{.User}}\n{{.URL}}"
  "pipeline": "🚦 {{ .Project }}: {{ .Action }}\n{{ get .Raw \"object_attributes.detailed_status\" }}"
template_files:
  "default": ./tmpl/gitlab-default.tmpl
```

### error_events → status=error

Ключи из `error_events` доставляются с BotX `notification.status=error`
(визуально выделяются в eXpress), остальные — `ok`. Матчинг — те же правила,
что и у фильтра (полный ключ, голый `kind`, `kind.*`).

```yaml
error_events: ["pipeline.failed", "build.failed"]
```

### Настройка express-botx

В отличие от Alertmanager/Grafana, эндпоинт включается только при наличии секции
`gitlab` с непустым списком отправителей:

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  gitlab:
    senders:
      - name: dev
        secret: env:GITLAB_WEBHOOK_TOKEN
        chats: [dev]
        events:
          only: ["merge_request.*", "pipeline.failed", "build.failed", push]
          exclude: ["merge_request.update"]
        templates:
          "merge_request.open": "🆕 {{.Title}} — {{.User}}\n{{.URL}}"
        # template_files:
        #   default: ./tmpl/gitlab-default.tmpl
        error_events: ["pipeline.failed", "build.failed"]
```

### Настройка GitLab

1. В группе или проекте: **Settings → Webhooks → Add new webhook**
2. **URL:** `http://express-botx:8080/api/v1/gitlab` (можно с `?chat_id=<alias>`)
3. **Secret token:** значение `server.gitlab.senders[].secret` нужной команды
4. Отметьте нужные триггеры (или все — фильтрация теперь на стороне приложения)
5. Сохраните и нажмите **Test → …**

### Роутинг событий по чатам (`routes`)

Каждый sender может задать свой опциональный **упорядоченный** список
`routes`, если цель зависит от проекта, event-ключа, ветки или payload.

**Модель матчинга (all-match + stop):**

- Срабатывают **все** совпавшие правила (не первое), их чаты объединяются и
  дедуплицируются с сохранением порядка.
- Правило с `stop: true`, совпав, обрывает дальнейший перебор.
- Правило состоит из условий `match` (селектор → список паттернов). Внутри одного
  селектора паттерны по **OR** (any-of), между селекторами — **AND**. Опущенный
  селектор ничего не ограничивает; пустой `match` — catch-all (совпадает всегда).
- Массив по dotted-пути матчится, если совпал **любой** его скалярный элемент.
  Элементы-объекты пропускаются: GitLab отдаёт `object_attributes.labels` как
  массив объектов `{title, color, …}`, поэтому напрямую по названию лейбла
  сматчить нельзя — матчатся только массивы скаляров.

**Контекст матчинга (селекторы):**

Зарезервированные селекторы — нормализованные поля события:

| Селектор | Значение |
|---|---|
| `kind` | `object_kind` |
| `event` | event-ключ (`kind` или `kind.subtype`) — матчится **event-матчером**, не паттернами |
| `action` | `object_attributes.action` или производный субтип |
| `project` | `project.path_with_namespace` (fallback `project.name`) — в шаблонах `.Project` остаётся коротким `project.name` |
| `branch` | нормализованная ветка (см. ниже) |
| `user` | `user.name` / `user.username` (fallback `user_name`) |
| `title` | `object_attributes.title` |
| `url` | `object_attributes.url` (fallback `project.web_url`) |

Любой другой селектор — **dotted-путь** в сыром payload (`view.Raw`): скаляр →
одно значение, массив → список строк, отсутствие поля → пусто (не матчится).

Нормализация `branch`: MR/`note` → `object_attributes.target_branch` (fallback
`merge_request.target_branch`); `push`/`tag_push` → ветка из `ref`
(`refs/heads/release/2.0` → `release/2.0`); `pipeline` →
`object_attributes.ref`; `build`/`job` → ветка из верхнеуровневого `ref`;
остальные типы → пусто.

**Паттерны:**

- По умолчанию — **glob** с `*` (любая последовательность символов, включая `/`):
  `group/backend/*`, `sec:*`, точное совпадение без `*`.
- Обёрнутый в слэши `/…/` — **регулярное выражение** (Go RE2, без ReDoS),
  компилируется на старте; битый regex роняет `serve`. Regex не заякорен —
  используйте `^…$` для полного совпадения.
- Селектор `event` использует event-матчинг (полный ключ `kind.subtype`, голый
  `kind`, `kind.*`), glob/regex к нему **не применяются**.

**Выбор чатов:** если есть `?chat_id=`, он обходит routes и выбирает subset внутри
скоупа. Без query вычисляются routes. Если ни одно правило не совпало,
возвращается `200 {"ok":true,"ignored":true,"event":"<key>","reason":"no route matched"}`.
Неявного fallback нет; для него нужно добавить последний catch-all `match: {}`.

**Фан-аут и коды ответа (best-effort):** сообщение отправляется в каждый целевой
чат независимо. Ответ — **единый** `MultiSendResponse`
`{"ok":true,"results":[{"chat","sync_id"}],"errors":[{"chat","error"}]}` с кодом
`200`, если доставлено **хотя бы в один** чат (частичные сбои — в `errors`);
`502`, если упали все. Явный `?chat_id=` (в т.ч. с запятой — фан-аут в несколько
чатов) и доставка без routes во все `chats` sender'а возвращают ту же форму — см.
[единый ответ](#единый-ответ-multisendresponse-ломающее-изменение).

```yaml
server:
  gitlab:
    senders:
      - name: backend
        secret: env:BACKEND_GITLAB_TOKEN
        chats: [backend, backend-mrs, releases, oncall]
        routes:
          - match:
              project: ["group/backend/*"]
              event: [merge_request]
              branch: [main, "release/*"]
            chats: [backend-mrs, releases]
            stop: true
          - match:
              event: ["pipeline.failed", "build.failed"]
              branch: ["/^(main|release\\/.*)$/"]
            chats: [oncall]
          - match: {}
            chats: [backend]
```

### Изоляция отправителей и скоуп чатов

Каждый sender аутентифицируется своим `secret` и обрабатывается только своими
`events`, `templates`, `template_files`, `error_events` и `routes`.

```yaml
server:
  gitlab:
    senders:
      - name: team-a
        secret: env:TEAM_A_GITLAB_TOKEN
        chats: [team-a, team-a-alerts]
      - name: team-b
        secret: env:TEAM_B_GITLAB_TOKEN
        # chats нет: scope выводится из объединения route-целей.
        routes:
          - match: {event: ["pipeline.failed"]}
            chats: [team-b-alerts]
          - match: {}
            chats: [team-b]
```

**Скоуп.** Если `chats` непуст, это явная граница, и все `routes[].chats` должны быть её
подмножеством. Если `chats` отсутствует или пуст, скоуп выводится из объединения
`routes[].chats`. Хотя бы один из списков должен быть непустым.

При старте алиасы канонизируются в UUID. Алиас и UUID одного чата эквивалентны при
проверке скоупа и дедупликации, но доставка сохраняет исходную цель и её bot
binding. Ошибки старта (их же ловит `config validate`):

- две разные цели одного сендера, резолвящиеся в один UUID;
- алиас без `id`;
- `route[].chats`, ссылающийся на алиас с bot binding, отличным от цели скоупа,
  в которую он канонизируется (иначе доставка молча ушла бы через другого бота);
- в multi-bot-режиме — raw UUID или алиас без `bot` среди delivery targets.

`?bot=` всегда игнорируется.

**`?chat_id=` внутри scope:**

- query отсутствует → вычисляются routes; если routes нет, доставка идёт во все `chats`;
- `?chat_id=a,b` (непустой) → доставка **только** в перечисленные чаты, если
  каждый разрешён; routes при этом не вычисляются;
- чат вне scope, **или** in-scope чат, названный алиасом с bot binding, отличным
  от цели сендера для этого чата → `403 Forbidden`, ничего не отправляется:
  первый случай — `chat "…" is outside this token's allowed chats`, второй —
  `chat "…" is bound to bot "…", but this token delivers to that chat via "…"`;
- `?chat_id` **явно пустой** (`?chat_id=`, `?chat_id=,,`) → `400` request-level,
  ничего не отправляется;
- сравнение эквивалентно по **алиасу и UUID**: `chats: [team-a-alerts]` разрешает
  и `?chat_id=team-a-alerts`, и UUID, в который этот алиас резолвится.

```
# routes sender'а или все его chats, если routes нет:
POST /api/v1/gitlab                        X-Gitlab-Token: <team-a-token>
# только один чат из scope:
POST /api/v1/gitlab?chat_id=team-a-alerts  X-Gitlab-Token: <team-a-token>  → 200
# чужой чат → отказ:
POST /api/v1/gitlab?chat_id=team-b         X-Gitlab-Token: <team-a-token>  → 403
# пустой chat_id → ошибка запроса:
POST /api/v1/gitlab?chat_id=               X-Gitlab-Token: <team-a-token>  → 400
```

**Правила конфигурации:**

- `senders` непуст; у каждого sender обязателен `secret` и хотя бы один из `chats`/`routes`;
- `name`, если задан, уникален;
- Значения токенов задаются ссылками `env:`/`vault:` — в общем YAML только
  ссылки, команды не видят секреты друг друга.
- Дубликаты **разрезолвленных** токенов между sender'ами —
  ошибка на старте: аутентификация была бы неоднозначной. Байт-идентичные
  строки в конфиге (один литерал или одна и та же `env:`/`vault:` ссылка
  дважды) ловит уже `config validate`.

В GitLab каждая команда настраивает свой webhook как обычно
([Настройка GitLab](#настройка-gitlab)), указывая в **Secret token** значение
своего sender-токена.

### Проверка вручную

```bash
curl -X POST "http://localhost:8080/api/v1/gitlab" \
  -H "X-Gitlab-Token: <secret token>" \
  -H "Content-Type: application/json" \
  -d '{
    "object_kind": "pipeline",
    "project": { "name": "backend" },
    "object_attributes": {
      "status": "failed",
      "url": "https://gitlab.company.ru/team/backend/-/pipelines/777"
    }
  }'
```

---

## Callback-ы от Express Platform

express-botx может принимать callback-и от сервера Express и маршрутизировать
их на внешние обработчики (скрипты или вебхуки). Это позволяет реагировать на
системные события (добавление в чат, вход/выход пользователя и др.) без написания
полноценного бота.

### Настройка express-botx

Добавьте секцию `callbacks` в конфигурацию сервера:

```yaml
server:
  listen: ":8080"
  callbacks:
    base_path: /botx
    verify_jwt: true
    rules:
      - events: [chat_created, added_to_chat]
        handler:
          type: exec
          command: ./on-membership.sh
          timeout: 10s
      - events: [notification_callback]
        handler:
          type: exec
          command: ./on-delivery.sh
      - events: ["*"]
        async: true
        handler:
          type: webhook
          url: http://my-service/events
          timeout: 30s
```

### Настройка Express Platform

В настройках бота на сервере Express укажите URL callback-а:

- **Command callback URL:** `http://express-botx:8080/botx/command`
- **Notification callback URL:** `http://express-botx:8080/botx/notification/callback`

### Пример exec-обработчика

```bash
#!/bin/bash
# on-membership.sh — обработка добавления в чат
EVENT="$EXPRESS_CALLBACK_EVENT"
CHAT_ID="$EXPRESS_CALLBACK_CHAT_ID"

echo "Event: $EVENT, Chat: $CHAT_ID"

# Полный JSON доступен через stdin
PAYLOAD=$(cat)
echo "$PAYLOAD" | jq .
```

### Пример webhook-обработчика

Webhook-обработчик получает POST-запрос с оригинальным JSON callback-а в теле.
Заголовки `X-Express-Event` и `X-Express-Sync-ID` содержат тип события и sync_id.

```python
# Flask-пример
from flask import Flask, request
app = Flask(__name__)

@app.route("/events", methods=["POST"])
def handle_event():
    event = request.headers.get("X-Express-Event")
    payload = request.get_json()
    print(f"Event: {event}, Payload: {payload}")
    return "", 200
```

### Sync vs Async

- **sync** (`async: false`, по умолчанию) — сервер ждёт завершения обработчика
  перед ответом клиенту. Подходит для быстрых обработчиков.
- **async** (`async: true`) — сервер сразу отвечает 202 и запускает обработчик
  в фоне. Подходит для долгих операций. При graceful shutdown сервер дожидается
  завершения фоновых обработчиков.

Подробнее о полях конфигурации — см. [Конфигурация](configuration.md#callback-ы-от-express-platform).

---

## Произвольные вебхуки через /send

Для систем без специальных эндпоинтов используйте `/send`. `chat_id` можно
указать через запятую для рассылки в несколько чатов; ответ — единый
`MultiSendResponse` (см. [Мульти-чат](#мульти-чат-и-единый-ответ-multisendresponse)):

```bash
# JSON — один чат
curl -X POST http://express-botx:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "chat_id": "deploy",
    "message": "Deploy v1.2.3 completed",
    "status": "ok"
  }'

# JSON — несколько чатов (fan-out)
curl -X POST http://express-botx:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "chat_id": "deploy,ops-alerts",
    "message": "Deploy v1.2.3 completed"
  }'
# → {"ok":true,"results":[{"chat":"deploy","sync_id":"..."},
#                         {"chat":"ops-alerts","sync_id":"..."}]}

# С файлом (multipart)
curl -X POST http://express-botx:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -F "chat_id=deploy" \
  -F "message=Отчёт за март" \
  -F "file=@report.pdf"
```

### Примеры интеграций

**GitLab CI:**

```yaml
notify:
  stage: notify
  script:
    - |
      curl -sf -X POST "$EXPRESS_BOTX_URL/api/v1/send" \
        -H "Authorization: Bearer $EXPRESS_BOTX_API_KEY" \
        -H "Content-Type: application/json" \
        -d "{\"chat_id\": \"deploy\", \"message\": \"Deploy $CI_PROJECT_NAME:$CI_COMMIT_TAG completed\"}"
```

**Jenkins Pipeline:**

```groovy
post {
    success {
        sh '''
            curl -sf -X POST "$EXPRESS_BOTX_URL/api/v1/send" \
              -H "Authorization: Bearer $EXPRESS_BOTX_API_KEY" \
              -H "Content-Type: application/json" \
              -d '{"chat_id": "deploy", "message": "Build #'"$BUILD_NUMBER"' OK"}'
        '''
    }
}
```

**Bash-скрипт (cron):**

```bash
#!/bin/bash
# Мониторинг места на диске
USAGE=$(df -h / | awk 'NR==2 {print $5}' | tr -d '%')
if [ "$USAGE" -gt 90 ]; then
    express-botx send --chat-id alerts --status error \
        "Диск заполнен на ${USAGE}% на $(hostname)"
fi
```
