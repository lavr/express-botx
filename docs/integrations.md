# Интеграции

Подключение express-botx к системам мониторинга.

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

Если нужно отправлять разные алерты в разные чаты, используйте разные receiver'ы с query-параметром `chat_id`:

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

Аналогично Alertmanager — создайте несколько contact point'ов с `?chat_id=`:

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
нельзя задать в обоих сразу.

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

Для систем без специальных эндпоинтов используйте `/send`:

```bash
# JSON
curl -X POST http://express-botx:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "chat_id": "deploy",
    "message": "Deploy v1.2.3 completed",
    "status": "ok"
  }'

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
