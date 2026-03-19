# RFC-008: Grafana Webhook Receiver

- **Статус:** Proposed
- **Дата:** 2026-03-13

## Контекст

Grafana Alerting поддерживает отправку уведомлений через webhook contact point. При срабатывании или разрешении алерта Grafana отправляет POST-запрос с JSON-описанием группы алертов.

Формат Grafana отличается от Alertmanager: другая структура корневого объекта (поля `orgId`, `truncatedAlerts`, `state`, `title`, `message`), расширенные поля в алертах (`values`, `fingerprint`, `silenceURL`, `dashboardURL`, `panelURL`, `imageURL`), другое именование статусов (`alerting`/`ok`/`no_data`/`pending` вместо `firing`/`resolved`).

Поэтому необходим отдельный эндпоинт, который понимает формат Grafana нативно.

## Предложение

Добавить эндпоинт `POST {basePath}/grafana` в HTTP-сервер (`serve`), который:

1. Принимает webhook от Grafana в его родном JSON-формате
2. Форматирует алерты через Go-шаблон
3. Отправляет сообщение в настроенный чат

### Формат Grafana webhook

```json
{
  "receiver": "express",
  "status": "firing",
  "orgId": 1,
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighCPU",
        "grafana_folder": "Production"
      },
      "annotations": {
        "summary": "CPU > 90% on web-01"
      },
      "startsAt": "2026-03-13T20:00:00.000Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://grafana:3000/alerting/grafana/abc123/view",
      "fingerprint": "abc123def456",
      "silenceURL": "http://grafana:3000/alerting/silence/new?...",
      "dashboardURL": "http://grafana:3000/d/dashboard-uid",
      "panelURL": "http://grafana:3000/d/dashboard-uid?viewPanel=1",
      "imageURL": "http://grafana:3000/render/...",
      "values": {
        "B": 95.2,
        "C": 1
      }
    }
  ],
  "groupLabels": {"alertname": "HighCPU"},
  "commonLabels": {"alertname": "HighCPU", "grafana_folder": "Production"},
  "commonAnnotations": {"summary": "CPU > 90% on web-01"},
  "externalURL": "http://grafana:3000",
  "version": "1",
  "groupKey": "{}:{alertname=\"HighCPU\"}",
  "truncatedAlerts": 0,
  "title": "[FIRING:1] HighCPU (Production)",
  "state": "alerting",
  "message": "**Firing**\n\nValue: ..."
}
```

### Отличия от Alertmanager

| Поле | Grafana | Alertmanager |
|------|---------|--------------|
| `orgId` | ID организации Grafana | — |
| `truncatedAlerts` | Кол-во обрезанных алертов | — |
| `title` | Готовый заголовок | — |
| `state` | `alerting` / `ok` / `no_data` / `pending` | — |
| `message` | Готовый текст (markdown) | — |
| `alerts[].values` | `map[string]any` — значения выражений | — |
| `alerts[].fingerprint` | Уникальный ID алерта | — |
| `alerts[].silenceURL` | Ссылка на silence | — |
| `alerts[].dashboardURL` | Ссылка на дашборд | — |
| `alerts[].panelURL` | Ссылка на панель | — |
| `alerts[].imageURL` | Ссылка на скриншот графика | — |
| `status` | `firing` / `resolved` | `firing` / `resolved` |

### Конфигурация

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  api_keys:
    - name: grafana
      key: env:GRAFANA_API_KEY
  grafana:
    default_chat_id: ops-alerts      # UUID или алиас (опционально)
    error_states:                    # state → status "error" (по умолчанию: [alerting])
      - alerting
    template: |                      # inline шаблон (опционально)
      ...
    template_file: grafana.tmpl     # путь к файлу шаблона (опционально)
```

### Выбор чата

Аналогично alertmanager-эндпоинту (RFC-007):

1. **Query parameter** `?chat_id=` — указывается в URL
2. **`default_chat_id`** — из конфига `server.grafana.default_chat_id`
3. **Единственный чат** — если в секции `chats` ровно один алиас

Если чат не определён ни одним из способов — возвращается 400 с подсказкой.

```yaml
# Настройка contact point в Grafana
contact_points:
  - name: express
    type: webhook
    settings:
      url: http://express-botx:8080/api/v1/grafana
      authorization_scheme: Bearer
      authorization_credentials: <api-key>

# Разные чаты для разных notification policy
  - name: express-ops
    type: webhook
    settings:
      url: http://express-botx:8080/api/v1/grafana?chat_id=ops-alerts
```

Приоритет шаблона: `template_file` > `template` > встроенный.

Путь `template_file` — относительно директории конфига (аналогично alertmanager).

### Встроенный шаблон по умолчанию

```
{{ if eq .Status "firing" }}🔥 FIRING{{ else }}✅ RESOLVED{{ end }} {{ .Title }}
{{ range .Alerts }}
{{ if eq .Status "firing" }}🔴{{ else }}🟢{{ end }} {{ index .Labels "alertname" }} — {{ index .Annotations "summary" }}
  Folder:   {{ index .Labels "grafana_folder" }}
  Started:  {{ .StartsAt.Format "2006-01-02 15:04:05" }}{{ if ne .Status "firing" }}
  Ended:    {{ .EndsAt.Format "2006-01-02 15:04:05" }}{{ end }}{{ if .DashboardURL }}
  Dashboard: {{ .DashboardURL }}{{ end }}{{ if .PanelURL }}
  Panel:     {{ .PanelURL }}{{ end }}{{ if .SilenceURL }}
  Silence:   {{ .SilenceURL }}{{ end }}
{{ end }}
```

### Данные, доступные в шаблоне

```go
type GrafanaWebhook struct {
    Receiver          string
    Status            string            // "firing" | "resolved"
    OrgID             int               `json:"orgId"`
    Alerts            []GrafanaAlertItem
    GroupLabels       map[string]string
    CommonLabels      map[string]string
    CommonAnnotations map[string]string
    ExternalURL       string
    Version           string
    GroupKey          string
    TruncatedAlerts   int
    Title             string            // готовый заголовок от Grafana
    State             string            // "alerting" | "ok" | "no_data" | "pending"
    Message           string            // готовый текст от Grafana (markdown)
}

type GrafanaAlertItem struct {
    Status       string                 // "firing" | "resolved"
    Labels       map[string]string
    Annotations  map[string]string
    StartsAt     time.Time
    EndsAt       time.Time
    GeneratorURL string
    Fingerprint  string
    SilenceURL   string
    DashboardURL string
    PanelURL     string
    ImageURL     string
    Values       map[string]any
}
```

### Маппинг state → status

Поле `status` в BotX API (`notification.status`):
- `"ok"` — обычное сообщение
- `"error"` — выделение как ошибка

Логика маппинга:
- Если `grafana.status == "resolved"` → `"ok"`
- Если `grafana.state` входит в `error_states` → `"error"`
- Иначе → `"ok"`

По умолчанию `error_states: [alerting]`.

**Отличие от alertmanager:** маппинг по корневому полю `state` (а не по `labels.severity` каждого алерта), т.к. Grafana предоставляет агрегированный статус группы.

### Настройка Grafana

В UI: Alerting → Contact points → New contact point:

- **Type:** Webhook
- **URL:** `http://express-botx:8080/api/v1/grafana`
- **HTTP Method:** POST
- **Authorization Header — Scheme:** Bearer
- **Authorization Header — Credentials:** `<api-key>`

Или через provisioning:

```yaml
# grafana/provisioning/alerting/contactpoints.yaml
apiVersion: 1
contactPoints:
  - orgId: 1
    name: express
    receivers:
      - uid: express-webhook
        type: webhook
        settings:
          url: http://express-botx:8080/api/v1/grafana
          httpMethod: POST
          authorization_scheme: Bearer
          authorization_credentials: $GRAFANA_EXPRESS_API_KEY
```

### Docker Compose пример

```yaml
services:
  express-botx:
    image: lavr/express-botx
    command: serve --config /config/config.yaml
    ports:
      - "8080:8080"
    volumes:
      - ./express-botx:/config

  grafana:
    image: grafana/grafana:11.5
    ports:
      - "3000:3000"
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning
```

## Эндпоинт

```
POST {basePath}/grafana[?chat_id=<uuid-or-alias>]
Content-Type: application/json
Authorization: Bearer <api-key>
```

### Ответы

| Код | Описание |
|-----|----------|
| 200 | Сообщение отправлено |
| 400 | Невалидный JSON, ошибка шаблона, или чат не указан |
| 401 | Нет авторизации |
| 403 | Неверный ключ |
| 502 | Ошибка отправки в eXpress |

Тело ответа:
```json
{"ok": true, "sync_id": "..."}
```

## Реализация

### Новые файлы

| Файл | Описание |
|------|----------|
| `internal/server/handler_grafana.go` | Хендлер, типы, встроенный шаблон, маппинг state→status |

### Изменения

| Файл | Описание |
|------|----------|
| `internal/config/config.go` | `GrafanaYAMLConfig` в `ServerConfig` |
| `internal/server/server.go` | `WithGrafana()`, регистрация маршрута |
| `internal/cmd/serve.go` | Загрузка шаблона, fallback на единственный чат |

### Порядок

1. Парсим конфиг `server.grafana`
2. Компилируем шаблон при старте сервера (fail fast при ошибке)
3. Определяем fallback chat (единственный алиас из `chats`)
4. Регистрируем `POST {basePath}/grafana` с auth middleware
5. При запросе:
   - Декодируем JSON в `GrafanaWebhook`
   - Рендерим шаблон → текст сообщения
   - Определяем `status` по `state`
   - Резолвим чат: `?chat_id` > `default_chat_id` > fallback
   - Отправляем через `SendFunc`

### Переиспользование кода

Структура хендлера аналогична alertmanager. Общая логика (выбор чата, отправка, формат ответа) может быть вынесена в shared-хелпер при необходимости, но на первом этапе дублирование минимально и предпочтительнее абстракции.

## Что НЕ включаем

- Загрузка изображений по `imageURL` — потребует HTTP-клиент и отправку файлов, отдельная задача
- HMAC-верификация подписи — Grafana webhook не подписывает запросы стандартно; авторизация через Bearer token достаточна
- Парсинг `message` (markdown от Grafana) — шаблон рендерит своё сообщение; поле `message` доступно в шаблоне при необходимости
