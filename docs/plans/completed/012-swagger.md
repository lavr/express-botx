# RFC-012: OpenAPI (Swagger) спецификация

- **Статус:** Proposed
- **Дата:** 2026-03-14

## Контекст

HTTP-сервер express-botx предоставляет несколько эндпоинтов (send, alertmanager, grafana, healthz), но у них нет формальной API-спецификации. Пользователи узнают формат запросов из README и RFC-документов.

Наличие OpenAPI-спецификации позволит:

- Генерировать интерактивную документацию (Swagger UI)
- Валидировать запросы на стороне клиента
- Генерировать клиентский код на любом языке
- Импортировать спецификацию в Postman, Insomnia и другие инструменты
- Тестировать API через Swagger UI прямо из браузера

## Предложение

Добавить статический файл `openapi.yaml` (OpenAPI 3.1) и опционально раздавать его и Swagger UI через сам сервер.

## Эндпоинты для документирования

### GET /healthz

Проверка здоровья. Без авторизации.

```yaml
responses:
  200:
    content:
      application/json:
        schema:
          type: object
          properties:
            ok:
              type: boolean
              example: true
```

### POST {basePath}/send

Отправка сообщения и/или файла. Поддерживает `application/json` и `multipart/form-data`.

**JSON body:**

| Поле | Тип | Обязательно | Описание |
|------|-----|-------------|----------|
| `chat_id` | string | да | UUID или алиас чата |
| `message` | string | да* | Текст сообщения |
| `file` | object | да* | Файл-вложение (`name`, `data` base64) |
| `status` | string | нет | `ok` (default) или `error` |
| `bot` | string | нет | Имя бота (обязательно в multi-bot режиме) |
| `opts` | object | нет | `silent`, `stealth`, `force_dnd`, `no_notify` |
| `metadata` | object | нет | Произвольный JSON |

*Обязательно хотя бы одно из `message` или `file`.

**Multipart fields:** `chat_id`, `message`, `file` (upload), `status`, `opts` (JSON), `metadata` (JSON).

**Responses:** 200, 400, 401, 403, 415, 502.

### POST {basePath}/alertmanager

Приём вебхуков от Alertmanager. Query param: `?chat_id=`.

**Body:** Alertmanager webhook JSON (version, groupKey, status, receiver, alerts[]).

**Responses:** 200, 400, 401, 403, 502.

### POST {basePath}/grafana

Приём вебхуков от Grafana. Query param: `?chat_id=`.

**Body:** Grafana webhook JSON (receiver, status, orgId, alerts[], title, state, message).

**Responses:** 200, 400, 401, 403, 502.

### Общие схемы

**Авторизация:**

```yaml
securitySchemes:
  bearerAuth:
    type: http
    scheme: bearer
  apiKeyHeader:
    type: apiKey
    in: header
    name: X-API-Key
  botSignature:
    type: apiKey
    in: header
    name: X-Bot-Signature
```

**Ответ (успех):**

```yaml
SendResponse:
  type: object
  properties:
    ok:
      type: boolean
      example: true
    sync_id:
      type: string
      example: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
```

**Ответ (ошибка):**

```yaml
ErrorResponse:
  type: object
  properties:
    ok:
      type: boolean
      example: false
    error:
      type: string
      example: "chat_id is required"
```

## Варианты размещения

### Вариант A: Статический файл + Swagger UI через сервер (рекомендуемый)

Сервер раздаёт спецификацию и Swagger UI:

```
GET {basePath}/docs           → Swagger UI (HTML)
GET {basePath}/openapi.yaml   → OpenAPI spec
```

Swagger UI встроен как embed (`embed.FS`), не требует внешних зависимостей в runtime.

**Конфигурация:**

```yaml
server:
  docs: false  # отключить /docs и /openapi.yaml (по умолчанию: true)
```

**Плюсы:**
- Документация доступна рядом с API
- Можно тестировать запросы через Swagger UI
- Не требует отдельного хостинга

**Минусы:**
- Swagger UI (~3MB) увеличивает бинарник при embed
- Дополнительный эндпоинт

### Вариант B: Только статический файл в репозитории

Файл `api/openapi.yaml` в репозитории. Swagger UI не раздаётся сервером — пользователи открывают спецификацию в Swagger Editor, Postman и т.п.

**Плюсы:**
- Минимально: один файл, без зависимостей
- Не увеличивает бинарник

**Минусы:**
- Нет встроенного UI
- Нужно вручную открывать в стороннем инструменте

### Вариант C: Генерация из аннотаций (swaggo)

Аннотации в Go-комментариях, спецификация генерируется через `swag init`:

```go
// @Summary Send a message
// @Tags send
// @Accept json
// @Produce json
// @Param body body SendPayload true "Message payload"
// @Success 200 {object} sendResponse
// @Router /send [post]
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
```

**Плюсы:**
- Спецификация всегда синхронизирована с кодом
- Популярный подход в Go

**Минусы:**
- Зависимость на `swaggo/swag` (генератор) и `swaggo/http-swagger` (UI middleware)
- Загромождение кода аннотациями
- Генерация требует отдельного шага (`go generate`)

## Рекомендация

**Вариант A** — статический `openapi.yaml` + встроенный Swagger UI, отключённый по умолчанию.

Причины:
1. Не загромождает код аннотациями
2. Спецификация написана вручную — более качественное описание
3. Swagger UI доступен из коробки для отладки
4. Включён по умолчанию — HTML ~500B + CDN, не влияет на производительность

## Реализация

### Файлы

| Файл | Описание |
|------|----------|
| `api/openapi.yaml` | OpenAPI 3.1 спецификация |
| `internal/server/docs.go` | Embed спецификации, Swagger UI handler |
| `internal/server/server.go` | Регистрация `/docs`, `/openapi.yaml` |

### Порядок

1. Написать `api/openapi.yaml` с описанием всех эндпоинтов
2. Встроить Swagger UI через `embed.FS` (swagger-ui-dist из npm или статическая копия)
3. Включить по умолчанию, отключение через `server.docs: false`
4. Зарегистрировать маршруты без авторизации (документация публична)
5. Добавить тест: GET `/docs` → 200, GET `/openapi.yaml` → 200 с валидным YAML

### Swagger UI: embed стратегия

Вместо embed всего swagger-ui-dist (~3MB) — использовать CDN-версию через минимальный HTML:

```html
<!DOCTYPE html>
<html>
<head>
  <title>express-botx API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({ url: "./openapi.yaml", dom_id: '#swagger-ui' });
  </script>
</body>
</html>
```

Это убирает проблему с размером бинарника — embed только `openapi.yaml` (~5KB) и `index.html` (~500B).

## Что НЕ включаем

- Валидация запросов по спецификации (middleware) — избыточно, хендлеры уже валидируют
- Генерация клиентского кода — пользователи делают сами из openapi.yaml
- Версионирование API — пока одна версия
