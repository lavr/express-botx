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
Помимо общего `secret` можно завести несколько изолированных токенов команд —
см. [senders](#изоляция-команд-senders-несколько-токенов).

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

Через `server.gitlab.events` можно ограничить, какие события отправляются.
Каждая запись фильтра матчит: полный event-ключ (`kind.subtype`), голый `kind`
(все субтипы этого типа) или wildcard `kind.*` (то же, что голый `kind`).

- `only` пустой → проходят все события; `only` непустой → событие должно матчиться.
- `exclude` вычитает и **всегда выигрывает** над `only`.
- Событие, не прошедшее фильтр → `200 OK` с телом
  `{"ok":true,"ignored":true,"event":"<key>"}` и без отправки сообщения.

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
`gitlab` с секретом:

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  gitlab:
    secret: env:GITLAB_WEBHOOK_TOKEN   # literal / env: / vault: — сверяется с X-Gitlab-Token
    default_chat_id: dev               # UUID или алиас чата (опционально)
    events:
      only:    ["merge_request.*", "pipeline.failed", "build.failed", "push"]
      exclude: ["merge_request.update"]
    templates:
      "merge_request.open": "🆕 {{.Title}} — {{.User}}\n{{.URL}}"
    # template_files:
    #   "default": ./tmpl/gitlab-default.tmpl
    error_events: ["pipeline.failed", "build.failed"]
```

Если `default_chat_id` не задан, используется чат по умолчанию (`default: true`),
единственный чат из конфига или query-параметр `?chat_id=`.

### Настройка GitLab

1. В группе или проекте: **Settings → Webhooks → Add new webhook**
2. **URL:** `http://express-botx:8080/api/v1/gitlab` (можно с `?chat_id=<alias>`)
3. **Secret token:** то же значение, что и `server.gitlab.secret`
4. Отметьте нужные триггеры (или все — фильтрация теперь на стороне приложения)
5. Сохраните и нажмите **Test → …**

### Несколько чатов

Как и у Alertmanager/Grafana — используйте `?chat_id=` в URL вебхука, чтобы
направлять события разных групп/проектов в разные чаты:

- `http://express-botx:8080/api/v1/gitlab?chat_id=backend-mrs`
- `http://express-botx:8080/api/v1/gitlab?chat_id=frontend-mrs`

### Роутинг событий по чатам (`routes`)

Когда одного `?chat_id`/`default_chat_id` мало (одно событие должно уходить в
несколько чатов, а выбор чата зависит от проекта, типа события или ветки),
используйте `server.gitlab.routes` — опциональный **упорядоченный** список
правил. Секция обратно совместима: без неё поведение = прежнее (один чат).

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

**Приоритет выбора чатов:** `?chat_id=` → `routes` (all-match+stop, дедуп) →
`default_chat_id` → чат по умолчанию → единственный чат → `200 {ignored}`.

**Фан-аут и коды ответа (best-effort):** сообщение отправляется в каждый целевой
чат независимо. Ответ — **единый** `MultiSendResponse`
`{"ok":true,"results":[{"chat","sync_id"}],"errors":[{"chat","error"}]}` с кодом
`200`, если доставлено **хотя бы в один** чат (частичные сбои — в `errors`);
`502`, если упали все. Явный `?chat_id=` (в т.ч. с запятой — фан-аут в несколько
чатов) и одиночный `default_chat_id` возвращают ту же форму (`results[0]` для
одного чата) — см. [единый ответ и ломающее изменение](#единый-ответ-multisendresponse-ломающее-изменение).

```yaml
server:
  gitlab:
    secret: env:GITLAB_WEBHOOK_TOKEN
    default_chat_id: dev          # fallback, если ни одно правило не совпало
    routes:
      # MR в main любого backend-проекта → в два чата; stop обрывает перебор.
      - match:
          project: ["group/backend/*"]
          event:   ["merge_request"]
          branch:  ["main", "release/*"]
        chats: [backend-mrs, releases]
        stop: true
      # Упавшие пайплайны и джобы → в дежурный чат (regex по ветке).
      - match:
          event:  ["pipeline.failed", "build.failed"]
          branch: ["/^(main|release\\/.*)$/"]
        chats: [oncall]
      # MR из веток hotfix/* (dotted-путь к скаляру в payload) → чат хотфиксов.
      - match:
          event: ["merge_request"]
          object_attributes.source_branch: ["hotfix/*"]
        chats: [hotfixes]
```

### Изоляция команд: senders (несколько токенов)

Когда один эндпоинт обслуживает несколько команд, `?chat_id`/`routes` не дают
изоляции: любой, кто знает общий секрет, может отправить событие в чужой чат.
`server.gitlab.senders` — опциональный список **дополнительных** входящих
токенов, каждый жёстко привязан к своему набору чатов:

```yaml
server:
  gitlab:
    secret: env:GITLAB_WEBHOOK_TOKEN     # общий дефолтный токен (опционален при senders)
    default_chat_id: dev
    senders:
      - secret: env:TEAM_A_GITLAB_TOKEN  # literal / env: / vault: — как обычный secret
        chats: [team-a]
      - secret: env:TEAM_B_GITLAB_TOKEN
        chats: [team-b, team-b-alerts]
```

**Резолв аутентификации.** Входящий `X-Gitlab-Token` сверяется со всеми
sender-токенами и с дефолтным `secret` (constant-time, без early-exit):

- совпал **sender-токен** → событие уходит в `chats` этого sender'а (fan-out,
  best-effort — как у `routes`). `?chat_id=` при этом работает как **фильтр
  внутри разрешённого набора** (см. ниже); `?bot=`, `routes` и `default_chat_id`
  **игнорируются** — команда A не может отправить от имени чужого бота или в чат
  вне своего scope, даже подставив `?bot`/`?chat_id`;
- совпал **дефолтный `secret`** → прежнее поведение без изменений
  (`?chat_id` → `routes` → `default_chat_id` → …);
- не совпал ни один → `401`.

Глобальные `events.only/exclude`, `templates`/`template_files` и `error_events`
применяются к sender-событиям как обычно (отфильтрованное событие → `200
{ignored}` и для sender'а). Своих `routes`/фильтров/шаблонов у sender'а нет —
его скоуп только чаты.

**`?chat_id=` как фильтр внутри scope.** Один sender-токен остаётся изолированным
в рамках команды, но команда может направлять конкретные вебхуки в нужный чат
внутри своего разрешённого набора:

- `?chat_id` **отсутствует** → событие уходит во **все** `chats` sender'а
  (прежнее поведение);
- `?chat_id=a,b` (непустой) → доставка **только** в перечисленные чаты, но
  **только если каждый** входит в `chats` sender'а (синтаксис общий: список через
  запятую, trim, dedup, сохранение порядка);
- хотя бы один чат из `?chat_id` **вне** `chats` sender'а → `403 Forbidden`
  (`{"ok":false,"error":"chat \"…\" is outside this token's allowed chats"}`),
  ничего не отправляется — попытка выйти за scope, а не тихое игнорирование;
- `?chat_id` **явно пустой** (`?chat_id=`, `?chat_id=,,`) → `400` request-level,
  ничего не отправляется;
- сравнение эквивалентно по **алиасу и UUID**: `chats: [team-a-alerts]` разрешает
  и `?chat_id=team-a-alerts`, и UUID, в который этот алиас резолвится (но не чужой
  алиас/UUID). Направление не важно — если в `chats` указан UUID, `?chat_id=`
  можно передать алиасом, и наоборот.

```
# всё разрешённое (оба чата sender'а):
POST /api/v1/gitlab                        X-Gitlab-Token: <team-a-token>
# только один чат из scope:
POST /api/v1/gitlab?chat_id=team-a-alerts  X-Gitlab-Token: <team-a-token>  → 200
# чужой чат → отказ:
POST /api/v1/gitlab?chat_id=team-b         X-Gitlab-Token: <team-a-token>  → 403
# пустой chat_id → ошибка запроса:
POST /api/v1/gitlab?chat_id=               X-Gitlab-Token: <team-a-token>  → 400
```

**Правила конфигурации:**

- Каждый sender: непустой `secret`/`secret_token` + непустой `chats`
  (алиасы/UUID существующих чатов из секции `chats`).
- Должен быть задан общий `secret` **или** хотя бы один sender — иначе ручка
  осталась бы без аутентификации (ошибка валидации).
- Дефолтный `secret` опционален: можно оставить только senders (тогда
  неизвестный токен всегда получает `401`), а можно смешанный режим — общий
  токен для большинства + изолированные senders для чувствительных команд.
- Значения токенов задаются ссылками `env:`/`vault:` — в общем YAML только
  ссылки, команды не видят секреты друг друга.
- Дубликаты **разрезолвленных** значений (sender↔sender или sender↔дефолт) —
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
