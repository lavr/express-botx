# RFC-007: Alertmanager Webhook Receiver

- **Статус:** Implemented
- **Дата:** 2026-03-13

## Контекст

Prometheus Alertmanager — стандартный инструмент для отправки уведомлений об инцидентах. Он поддерживает webhook-интеграцию: при срабатывании или разрешении алерта отправляет POST-запрос с JSON-описанием группы алертов.

Сейчас для отправки алертов в eXpress через express-botx нужен промежуточный скрипт или сервис, который преобразует формат Alertmanager в вызов `/api/v1/send`. Это лишнее звено в цепочке.

## Предложение

Добавить нативный эндпоинт `POST {basePath}/alertmanager` в HTTP-сервер (`serve`), который:

1. Принимает webhook от Alertmanager в его родном JSON-формате
2. Форматирует алерты через Go-шаблон
3. Отправляет сообщение в настроенный чат

### Формат Alertmanager webhook

```json
{
  "version": "4",
  "groupKey": "{}:{alertname=\"HighCPU\"}",
  "status": "firing",
  "receiver": "express",
  "groupLabels": {"alertname": "HighCPU"},
  "commonLabels": {"alertname": "HighCPU", "env": "prod"},
  "commonAnnotations": {"summary": "CPU is too high"},
  "externalURL": "http://alertmanager:9093",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighCPU",
        "severity": "critical",
        "instance": "web-01:9090"
      },
      "annotations": {
        "summary": "CPU > 90% on web-01",
        "description": "CPU usage is 95% for 5 minutes"
      },
      "startsAt": "2026-03-13T20:00:00.000Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://prometheus:9090/graph?g0.expr=..."
    }
  ]
}
```

### Конфигурация

```yaml
server:
  listen: ":8080"
  base_path: /api/v1
  api_keys:
    - name: alertmanager
      key: env:ALERTMANAGER_API_KEY
  alertmanager:
    default_chat_id: ops-alerts      # UUID или алиас (опционально)
    error_severities:                # severity → status "error" (по умолчанию: [critical, warning])
      - critical
      - warning
    template: |                      # inline шаблон (опционально)
      ...
    template_file: alertmanager.tmpl # путь к файлу шаблона (опционально)
```

### Выбор чата

Приоритет (от высшего к низшему):

1. **Query parameter** `?chat_id=` — указывается в URL при вызове эндпоинта
2. **`default_chat_id`** — из конфига `server.alertmanager.default_chat_id`
3. **Единственный чат** — если в секции `chats` ровно один алиас, он используется автоматически (аналогично поведению `express-botx send`)

Если чат не определён ни одним из способов — возвращается 400 с подсказкой.

Это позволяет гибко настраивать Alertmanager:

```yaml
# Один receiver — дефолтный чат из конфига
receivers:
  - name: express
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager

# Разные чаты для разных route
receivers:
  - name: express-ops
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager?chat_id=ops-alerts
  - name: express-dev
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager?chat_id=dev-alerts
```

Приоритет шаблона: `template_file` > `template` > встроенный.

Путь `template_file` — относительно директории конфига (если не абсолютный). Т.е. при конфиге `/etc/express-botx/config.yaml` путь `alertmanager.tmpl` резолвится в `/etc/express-botx/alertmanager.tmpl`.

### Встроенный шаблон по умолчанию

```
{{ if eq .Status "firing" }}🔥 FIRING{{ else }}✅ RESOLVED{{ end }} [{{ index .GroupLabels "alertname" }}]

{{ range .Alerts }}{{ if eq .Status "firing" }}🔴{{ else }}🟢{{ end }} {{ index .Labels "alertname" }} — {{ index .Annotations "summary" }}
  Severity: {{ index .Labels "severity" }}
  Instance: {{ index .Labels "instance" }}
  Started:  {{ .StartsAt.Format "2006-01-02 15:04:05" }}
{{ if ne .Status "firing" }}  Ended:    {{ .EndsAt.Format "2006-01-02 15:04:05" }}
{{ end }}{{ end }}
```

### Данные, доступные в шаблоне

```go
type AlertmanagerWebhook struct {
    Version           string
    GroupKey          string
    Status            string            // "firing" | "resolved"
    Receiver          string
    GroupLabels       map[string]string
    CommonLabels      map[string]string
    CommonAnnotations map[string]string
    ExternalURL       string
    Alerts            []AlertItem
}

type AlertItem struct {
    Status       string            // "firing" | "resolved"
    Labels       map[string]string
    Annotations  map[string]string
    StartsAt     time.Time
    EndsAt       time.Time
    GeneratorURL string
}
```

Поскольку `GroupLabels`, `Labels`, `Annotations` — это `map[string]string`, в шаблонах используется `index .Labels "key"` вместо `.Labels.key`.

### Маппинг severity → status

Поле `status` в BotX API (`notification.status`) определяет визуальное оформление сообщения:
- `"ok"` — обычное сообщение
- `"error"` — выделение как ошибка

Логика маппинга:
- Если `alertmanager.status == "resolved"` → `"ok"`
- Если любой алерт в группе имеет `labels.severity` из `error_severities` → `"error"`
- Иначе → `"ok"`

По умолчанию `error_severities: [critical, warning]`.

### Настройка Alertmanager

```yaml
# alertmanager.yml
route:
  receiver: express
  group_by: [alertname, env]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h

receivers:
  - name: express
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager
        send_resolved: true
        http_config:
          bearer_token_file: /etc/alertmanager/express-api-key
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

  alertmanager:
    image: prom/alertmanager
    volumes:
      - ./alertmanager.yml:/etc/alertmanager/alertmanager.yml
```

## Эндпоинт

```
POST {basePath}/alertmanager[?chat_id=<uuid-or-alias>]
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
| `internal/server/handler_alertmanager.go` | Хендлер, типы, встроенный шаблон, маппинг severity→status |

### Изменения

| Файл | Описание |
|------|----------|
| `internal/config/config.go` | `AlertmanagerYAMLConfig` в `ServerConfig` |
| `internal/server/server.go` | `Option`/`WithAlertmanager()`, регистрация маршрута |
| `internal/cmd/serve.go` | Загрузка шаблона, fallback на единственный чат |

### Порядок

1. Парсим конфиг `server.alertmanager`
2. Компилируем шаблон при старте сервера (fail fast при ошибке)
3. Определяем fallback chat (единственный алиас из `chats`)
4. Регистрируем `POST {basePath}/alertmanager` с auth middleware
5. При запросе:
   - Декодируем JSON в `AlertmanagerWebhook`
   - Рендерим шаблон → текст сообщения
   - Определяем `status` по severity
   - Резолвим чат: `?chat_id` > `default_chat_id` > fallback
   - Отправляем через `SendFunc`

## Что НЕ включаем

- Отправка файлов/графиков — потребует интеграцию с Grafana, отдельная задача
- Дедупликация/группировка — Alertmanager уже делает это
- Шаблоны для разных alertname — один шаблон на все алерты; кастомизация через Go template conditionals
