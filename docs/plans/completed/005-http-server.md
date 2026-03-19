# RFC-005: HTTP-сервер (субкоманда serve)

- **Статус:** Draft
- **Дата:** 2026-03-13

## Контекст

`express-botx` — CLI-инструмент для отправки сообщений в eXpress. Для интеграции с внешними системами (мониторинг, CI/CD, скрипты) нужен HTTP-интерфейс, через который можно отправлять сообщения без вызова CLI.

## Решение

Новая субкоманда `express-botx serve`, запускающая HTTP-сервер. Переиспользует всю существующую инфраструктуру: конфиг, auth, token cache, botapi-клиент.

### Почему субкоманда, а не отдельный бинарник

- Вся инфраструктура (конфиг, secret resolution, token cache, botapi) уже в `internal/`
- Единая точка сборки и деплоя
- Консистентно с архитектурой (`send`, `chats`, `bot`, `user`, `serve`)

## Конфигурация

### Секция `server` в config.yaml

```yaml
server:
  listen: ":8080"
  base_path: "/api/v1"
  api_keys:
    - name: monitoring
      key: "env:MONITORING_API_KEY"
    - name: ci-pipeline
      key: "vault:secret/express-send#ci_key"
    - name: manual
      key: "a1b2c3d4e5f6"
```

| Поле | Тип | Описание | По умолчанию |
|------|-----|----------|:---:|
| `listen` | string | Адрес и порт для прослушивания | `:8080` |
| `base_path` | string | Префикс для всех API-путей | `/api/v1` |
| `api_keys` | list | Список API-ключей для аутентификации | — |
| `api_keys[].name` | string | Имя клиента (для логов, не для auth) | — |
| `api_keys[].key` | string | Ключ. Поддерживает `env:VAR`, `vault:path#key`, литерал | — |
| `allow_bot_secret_auth` | bool | Разрешить аутентификацию через HMAC-подпись бота | `false` |

### Переменные окружения

| Переменная | Описание |
|------------|----------|
| `EXPRESS_SERVER_LISTEN` | Адрес (`":8080"`, `"127.0.0.1:9090"`) |
| `EXPRESS_SERVER_BASE_PATH` | Префикс путей (`"/api/v1"`, `"/express"`) |
| `EXPRESS_SERVER_API_KEY` | Единственный ключ (упрощённый вариант вместо списка) |

### CLI-флаги

```bash
express-botx serve [flags]

  --listen ADDR       Адрес для прослушивания (переопределяет конфиг)
  --api-key KEY       API-ключ (переопределяет конфиг, для быстрого старта)
```

Приоритет: CLI-флаг > env > config.yaml.

## Аутентификация

### Способ 1: API-ключи (рекомендуемый)

Поддерживаются два формата заголовков (проверяются по одному списку ключей):

```
Authorization: Bearer <key>
X-API-Key: <key>
```

Сервер извлекает ключ из любого из заголовков, ищет совпадение в списке `api_keys`. При совпадении — запрос авторизован, `name` ключа логируется.

### Способ 2: Секрет бота (bot_secret)

Клиент передаёт HMAC-подпись, вычисленную из `bot_id` и `secret_key` — те же credentials, что используются для получения токена BotX API.

```
X-Bot-Signature: <HMAC-SHA256(key=secret_key, msg=bot_id)>
```

Сервер вычисляет ожидаемую подпись из своего конфига и сравнивает с переданной.

Этот режим **отключён по умолчанию** и включается явно:

```yaml
server:
  allow_bot_secret_auth: true
```

**Предупреждение:** этот способ менее безопасен, чем API-ключи:

- **Утечка подписи = полный доступ к боту.** API-ключ ограничивает клиента эндпоинтами сервера. Секрет бота даёт прямой доступ к CTS API (чтение чатов, пользователей, отправка от имени бота).
- **Нет разграничения клиентов.** Секрет один — невозможно логировать, какой именно клиент сделал запрос.
- **Ротация затрагивает всё.** Замена секрета требует обновления в CTS-платформе. Ротация API-ключа — только в конфиге сервера.

Рекомендуется использовать только для простых сценариев, где клиент уже знает секрет бота и дополнительные ключи создают лишнюю сложность.

### Порядок проверки

1. `Authorization: Bearer <key>` или `X-API-Key: <key>` — проверка по списку `api_keys`
2. `X-Bot-Signature: <sig>` — проверка подписи (только если `allow_bot_secret_auth: true`)
3. Если ни один способ не сработал — 401/403

### Запуск без ключей

Если `api_keys` пуст, `--api-key` не передан и `allow_bot_secret_auth` отключён — сервер отказывается стартовать (нельзя запускать без аутентификации).

### Middleware

```go
func (s *Server) authMiddleware(next http.Handler) http.Handler
```

- 401 если заголовок отсутствует
- 403 если ключ/подпись не прошли проверку
- В `context` кладётся `name` ключа (или `"bot_secret"`) для логирования

## API

### POST /api/v1/send

Отправка сообщения и/или файла в чат. Поддерживает два формата передачи.

#### Формат 1: JSON (`Content-Type: application/json`)

```json
{
  "chat_id": "uuid-or-alias",
  "message": "Текст сообщения",
  "file": {
    "name": "report.pdf",
    "data": "<base64>"
  },
  "status": "ok",
  "opts": {
    "silent": false,
    "stealth": false,
    "force_dnd": false,
    "no_notify": false
  },
  "metadata": {"ticket_id": 42}
}
```

| Поле | Тип | Обязательное | Описание |
|------|-----|:---:|----------|
| `chat_id` | string | да | UUID чата или алиас из конфига |
| `message` | string | нет* | Текст сообщения |
| `file` | object | нет* | Вложение |
| `file.name` | string | да (если file) | Имя файла |
| `file.data` | string | да (если file) | Содержимое, base64 |
| `status` | string | нет | `"ok"` (default) или `"error"` |
| `opts` | object | нет | Опции доставки |
| `metadata` | object | нет | Произвольный JSON |

\* Нужен хотя бы `message` или `file`.

#### Формат 2: Multipart (`Content-Type: multipart/form-data`)

| Поле | Тип | Обязательное | Описание |
|------|-----|:---:|----------|
| `chat_id` | form field | да | UUID чата или алиас |
| `message` | form field | нет* | Текст сообщения |
| `file` | file part | нет* | Файл (имя берётся из заголовка `Content-Disposition`) |
| `status` | form field | нет | `"ok"` (default) или `"error"` |
| `opts` | form field | нет | JSON-строка с опциями доставки |
| `metadata` | form field | нет | JSON-строка |

\* Нужен хотя бы `message` или `file`.

Примеры:

```bash
# Только текст
curl -X POST http://localhost:8080/api/v1/send \
  -H "Authorization: Bearer <key>" \
  -F chat_id=alerts \
  -F message="Деплой завершён"

# Файл + текст
curl -X POST http://localhost:8080/api/v1/send \
  -H "Authorization: Bearer <key>" \
  -F chat_id=alerts \
  -F message="Отчёт за март" \
  -F file=@report.pdf

# Файл без текста
curl -X POST http://localhost:8080/api/v1/send \
  -H "X-API-Key: <key>" \
  -F chat_id=alerts \
  -F file=@screenshot.png
```

#### Выбор формата

Обработчик определяет формат по заголовку `Content-Type`:
- `application/json` — парсит JSON-тело
- `multipart/form-data` — парсит form fields + file part
- Иное — 415 Unsupported Media Type

**Ответ (200):**

```json
{
  "ok": true,
  "sync_id": "019ed0f1-c6bb-566e-9749-e744caef45c8"
}
```

**Ошибки:**

| HTTP | Тело | Причина |
|------|------|---------|
| 400 | `{"ok": false, "error": "message or file required"}` | Пустой запрос |
| 400 | `{"ok": false, "error": "chat not found: alerts"}` | Невалидный алиас |
| 401 | `{"ok": false, "error": "unauthorized"}` | Нет заголовка авторизации |
| 403 | `{"ok": false, "error": "forbidden"}` | Невалидный ключ |
| 502 | `{"ok": false, "error": "upstream error: ..."}` | Ошибка BotX API |

### GET /healthz

Health-check без аутентификации.

**Ответ (200):**

```json
{"ok": true}
```

## Использование

```bash
# Минимальный запуск
express-botx serve --api-key my-secret-key

# С конфигом
express-botx serve

# Переопределение адреса
express-botx serve --listen 127.0.0.1:9090

# Отправка через curl
curl -X POST http://localhost:8080/api/v1/send \
  -H "Authorization: Bearer my-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"chat_id": "alerts", "message": "CPU > 90%"}'
```

## Изменения

### Новые файлы

| Файл | Описание |
|------|----------|
| `internal/cmd/serve.go` | Субкоманда `serve`: парсинг флагов, запуск сервера |
| `internal/server/server.go` | HTTP-сервер, роутинг, middleware |
| `internal/server/handler_send.go` | Обработчик `POST /api/v1/send` |
| `internal/server/auth.go` | Auth middleware (Bearer + X-API-Key) |

### Модификации

| Файл | Описание |
|------|----------|
| `internal/cmd/cmd.go` | Добавить `case "serve"` в диспетчер |
| `internal/config/config.go` | Добавить `ServerConfig` с `Listen` и `APIKeys` |

### Структуры конфига

```go
type ServerConfig struct {
    Listen   string      `yaml:"listen"`
    BasePath string      `yaml:"base_path"`
    APIKeys  []APIKeyDef `yaml:"api_keys"`
}

type APIKeyDef struct {
    Name string `yaml:"name"`
    Key  string `yaml:"key"` // literal, env:VAR, vault:path#key
}
```

`Key` резолвится через существующий `internal/secret/` при старте сервера.

## Что НЕ включаем в P0

| Фича | Причина |
|------|---------|
| TLS termination | Перед сервисом будет reverse proxy / ingress |
| Rate limiting | Внутренний сервис, можно добавить позже |
| Batch send (несколько чатов) | Отдельный RFC |
| WebSocket / SSE | Нет use-case |
| CORS | API для бэкендов, не для браузеров |

## Graceful shutdown

Сервер обрабатывает `SIGINT` / `SIGTERM`:
1. Прекращает принимать новые соединения
2. Ждёт завершения текущих запросов (таймаут 10 секунд)
3. Выходит с кодом 0

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```

## Проверка

1. `express-botx serve --api-key test` — стартует на `:8080`
2. `curl localhost:8080/healthz` — 200
3. `curl -H "Authorization: Bearer test" -d '{"chat_id":"...", "message":"hi"}' localhost:8080/api/v1/send` — 200
4. `curl -H "X-API-Key: test" -d '...' localhost:8080/api/v1/send` — 200
5. `curl -d '...' localhost:8080/api/v1/send` — 401
6. `curl -H "X-API-Key: wrong" -d '...' localhost:8080/api/v1/send` — 403
7. `curl -H "X-API-Key: test" -d '{}' localhost:8080/api/v1/send` — 400
8. `express-botx serve` (без ключей в конфиге) — ошибка запуска
9. `kill -TERM <pid>` — graceful shutdown
