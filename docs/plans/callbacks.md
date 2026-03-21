# План: Поддержка Callback-ов от Express Platform (BotX API v4)

## Цель

Добавить в режим `serve` возможность принимать callback-и от сервера Express
(эндпоинты `/command` и `/notification/callback`), маршрутизировать их по
конфигурируемым правилам на внешние обработчики (exec / webhook), и предоставить
расширяемый интерфейс для разработчиков, использующих `express-botx` как
библиотеку.

---

## Справка

### Формат конфига

```yaml
server:
  callbacks:
    base_path: /botx            # отдельный base path (по умолчанию = server.base_path)
    verify_jwt: true            # JWT-верификация включена по умолчанию
    rules:
      - events: [chat_created, added_to_chat]
        async: false            # sync — ждём завершения перед ответом 202
        verify_jwt: false       # для конкретного правила можно переопределить дефолтный verify_jwt
        handler:
          type: exec
          command: ./on-membership.sh
          timeout: 10s
      - events: [cts_login, cts_logout]
        async: true             # async — 202 сразу, обработчик в фоне
        handler:
          type: webhook
          url: http://my-service/auth-events
          timeout: 30s
      - events: [notification_callback]
        handler:
          type: exec
          command: ./on-delivery.sh
      - events: ["*"]           # catch-all для остальных событий
        async: true
        handler:
          type: exec
          command: ./fallback.sh
```

### Типы событий

На `POST /command`:

| Событие | `command.body` |
|---|---|
| `message` | обычный текст (не `system:*`) |
| `chat_created` | `system:chat_created` |
| `added_to_chat` | `system:added_to_chat` |
| `user_joined_to_chat` | `system:user_joined_to_chat` |
| `deleted_from_chat` | `system:deleted_from_chat` |
| `left_from_chat` | `system:left_from_chat` |
| `chat_deleted_by_user` | `system:chat_deleted_by_user` |
| `cts_login` | `system:cts_login` |
| `cts_logout` | `system:cts_logout` |
| `event_edit` | `system:event_edit` |
| `smartapp_event` | `system:smartapp_event` |
| `internal_bot_notification` | `system:internal_bot_notification` |
| `conference_created` | `system:conference_created` |
| `conference_deleted` | `system:conference_deleted` |
| `call_started` | `system:call_started` |
| `call_ended` | `system:call_ended` |

На `POST /notification/callback`:

| Событие | Источник |
|---|---|
| `notification_callback` | `POST /notification/callback` |

### Передача данных обработчикам

**exec:**
- **stdin**: полный JSON тела callback-а
- **env-переменные**:
  - `EXPRESS_CALLBACK_EVENT` — тип события
  - `EXPRESS_CALLBACK_SYNC_ID` — sync_id запроса
  - `EXPRESS_CALLBACK_BOT_ID` — bot_id из payload
  - `EXPRESS_CALLBACK_CHAT_ID` — group_chat_id из `from`
  - `EXPRESS_CALLBACK_USER_HUID` — user_huid из `from` (если есть)
- **exit code**: 0 = успех, != 0 = ошибка (логируется)

**webhook:**
- **POST** на указанный URL
- **Body**: оригинальный JSON callback-а
- **Headers**: `Content-Type: application/json`, `X-Express-Event`, `X-Express-Sync-ID`
- **Ожидаемый ответ**: HTTP 2xx = успех

### Расширяемость (библиотечный интерфейс)

```go
type CallbackHandler interface {
    Type() string
    Handle(ctx context.Context, event string, payload []byte) error
}
```

Подключение custom-обработчиков:
```go
srv := server.New(cfg, sendFn, chatResolver,
    server.WithCallbacks(callbacksCfg,
        server.WithCallbackHandler(myCustomHandler),
    ),
)
```

---

## Section 1: Конфиг и типы

### 1.1 YAML-структуры конфига

- [x] Добавить `CallbacksConfig` в `internal/config/config.go`:
  поля `BasePath`, `VerifyJWT *bool`, `Rules []CallbackRule`
- [x] Добавить `CallbackRule`: поля `Events []string`, `Async bool`,
  `Handler CallbackHandlerConfig`
- [x] Добавить `CallbackHandlerConfig`: поля `Type string`,
  `Command string`, `URL string`, `Timeout string`
- [x] Добавить `Callbacks *CallbacksConfig` в `ServerConfig`

```
go build ./...
```

### 1.2 Валидация конфига

- [x] Валидация при парсинге: events не пустой, handler.type один из
  `exec`/`webhook`, command/url заполнены в зависимости от type
- [x] Валидация events: предупреждение (не ошибка) при неизвестном имени
  события (чтобы не ломать custom events в будущем)
- [x] Валидация timeout: парсится через `time.ParseDuration`

```
go test ./internal/config/ -run TestCallbacksConfig -v
```

### 1.3 Интерфейс CallbackHandler

- [x] Создать `internal/server/callback.go`
- [x] Определить интерфейс `CallbackHandler` (`Type() string`,
  `Handle(ctx, event, payload) error`)

```
go build ./internal/server/
```

### 1.4 Типы событий и парсинг

- [x] В `internal/server/callback.go` определить строковые константы для
  всех 16 system-событий + `message` + `notification_callback`
- [x] Функция `parseEventType(commandBody string) string` —
  `"system:chat_created"` → `"chat_created"`, обычный текст → `"message"`

```
go test ./internal/server/ -run TestParseEventType -v
```

### 1.5 Структуры BotX API v4 payload

- [x] В `internal/server/callback.go` определить `CallbackPayload`:
  `SyncID`, `Command` (Body, CommandType, Data, Metadata),
  `From` (UserHUID, GroupChatID, Host, ...), `BotID`, `ProtoVersion`
- [x] Определить `NotificationCallbackPayload`:
  `SyncID`, `Status`, `Result`, `Reason`, `Errors`, `ErrorData`

```
go build ./internal/server/
```

---

## Section 2: Обработчики

### 2.1 ExecHandler — базовая реализация

- [x] Создать `internal/server/callback_exec.go`
- [x] `NewExecHandler(command string, timeout time.Duration) *ExecHandler`
- [x] `Handle()`: запуск через `exec.CommandContext`, JSON → stdin
- [x] `Type()` → `"exec"`

```
go build ./internal/server/
```

### 2.2 ExecHandler — env-переменные

- [x] Парсинг payload для извлечения метаданных (sync_id, bot_id,
  group_chat_id, user_huid)
- [x] Установка env: `EXPRESS_CALLBACK_EVENT`, `EXPRESS_CALLBACK_SYNC_ID`, `EXPRESS_CALLBACK_BOT_ID`,
  `EXPRESS_CALLBACK_CHAT_ID`, `EXPRESS_CALLBACK_USER_HUID`

```
go test ./internal/server/ -run TestExecHandler -v
```

### 2.3 ExecHandler — логирование и ошибки

- [x] Capture stdout/stderr внешней команды → `vlog.Debug` / `vlog.Warn`
- [x] Ненулевой exit code → error с содержимым stderr
- [x] Timeout → context.DeadlineExceeded → kill процесса

```
go test ./internal/server/ -run TestExecHandlerTimeout -v
```

### 2.4 WebhookHandler — реализация

- [x] Создать `internal/server/callback_webhook.go`
- [x] `NewWebhookHandler(url string, timeout time.Duration) *WebhookHandler`
- [x] `Handle()`: HTTP POST, body = оригинальный JSON
- [x] Headers: `Content-Type`, `X-Express-Event`, `X-Express-Sync-ID`
- [x] `Type()` → `"webhook"`

```
go test ./internal/server/ -run TestWebhookHandler -v
```

### 2.5 WebhookHandler — ошибки

- [x] HTTP response не 2xx → error с кодом и телом ответа (truncated)
- [x] Timeout → context deadline → error
- [x] Connection refused → error с понятным сообщением

```
go test ./internal/server/ -run TestWebhookHandlerErrors -v
```

---

## Section 3: Маршрутизация

### 3.1 CallbackRouter — структура

- [x] Создать `internal/server/callback_router.go`
- [x] `type matchedRule struct { handler CallbackHandler; async bool }`
- [x] `type CallbackRouter struct` — хранит правила и handler registry
- [x] `NewCallbackRouter(rules, handlerRegistry) (*CallbackRouter, error)`

```
go build ./internal/server/
```

### 3.2 CallbackRouter — маршрутизация

- [ ] `Route(event string) []matchedRule` — возвращает все совпавшие правила
- [ ] Конкретные events матчат точно, `"*"` матчит любое событие
- [ ] Правила проверяются в порядке объявления, все совпавшие выполняются

```
go test ./internal/server/ -run TestCallbackRouter -v
```

### 3.3 Handler registry — сборка из конфига

- [ ] Функция `buildHandlers(rules []config.CallbackRule) (map[int]CallbackHandler, error)`
- [ ] Создаёт ExecHandler / WebhookHandler по каждому правилу
- [ ] Поддержка custom handlers через registry (для библиотечного API)

```
go test ./internal/server/ -run TestBuildHandlers -v
```

---

## Section 4: JWT-верификация

### 4.1 JWT парсинг и проверка подписи

- [ ] Создать `internal/server/callback_jwt.go`
- [ ] Функция `verifyCallbackJWT(tokenString string, secretLookup func(botID string) (string, error)) error`
- [ ] Парсинг JWT, проверка алгоритма = HS256
- [ ] Проверка подписи через bot secret (lookup по `aud` claim)

```
go test ./internal/server/ -run TestVerifyCallbackJWT -v
```

### 4.2 JWT claims валидация

- [ ] Проверка `exp` — не просрочен
- [ ] Проверка `nbf` — уже валиден
- [ ] Проверка `aud` — совпадает с известным bot_id
- [ ] Reject неподписанных JWT (alg: none)

```
go test ./internal/server/ -run TestJWTClaims -v
```

### 4.3 JWT middleware

- [ ] `callbackJWTMiddleware(h http.Handler, secretLookup, verifyEnabled bool) http.Handler`
- [ ] Извлечение `Authorization: Bearer <token>` из заголовка
- [ ] При `verify_jwt: false` — пропускать проверку
- [ ] При ошибке — HTTP 401 с JSON-ответом

```
go test ./internal/server/ -run TestJWTMiddleware -v
```

---

## Section 5: HTTP-эндпоинты

### 5.1 handleCommand — POST /command

- [ ] Создать `internal/server/handler_callback.go`
- [ ] Парсинг JSON body → `CallbackPayload`
- [ ] Определение типа события через `parseEventType`
- [ ] Маршрутизация через `CallbackRouter.Route(event)`
- [ ] Ответ `{"result": "accepted"}` с кодом 202

```
go test ./internal/server/ -run TestHandleCommand -v
```

### 5.2 handleCommand — sync/async выполнение

- [ ] sync-правила: выполнить handler, дождаться, потом 202
- [ ] async-правила: запустить goroutine, сразу 202
- [ ] Ошибка обработчика (sync) → лог + 202 (не блокируем Express)
- [ ] Ошибка обработчика (async) → лог + error tracker

```
go test ./internal/server/ -run TestHandleCommandAsync -v
```

### 5.3 handleNotificationCallback — POST /notification/callback

- [ ] Парсинг JSON body → `NotificationCallbackPayload`
- [ ] Маршрутизация как событие `notification_callback`
- [ ] Ответ `{"result": "ok"}` с кодом 200

```
go test ./internal/server/ -run TestHandleNotificationCallback -v
```

### 5.4 Нет совпавших правил

- [ ] Если ни одно правило не совпало → 202 без вызова обработчиков
- [ ] Debug-лог: `"no matching rules for event %s"`

```
go test ./internal/server/ -run TestHandleCommandNoRules -v
```

---

## Section 6: Интеграция

### 6.1 Server Option — WithCallbacks

- [ ] В `server.go` добавить `WithCallbacks(cfg CallbacksConfig, opts ...CallbackOption) Option`
- [ ] `CallbackOption` тип: `WithCallbackHandler(handler CallbackHandler)`
- [ ] При вызове: создать router, зарегистрировать handlers

```
go build ./internal/server/
```

### 6.2 Регистрация эндпоинтов в Server.New

- [ ] Если callbacks сконфигурированы — зарегистрировать
  `POST <callbacks_base_path>/command` и
  `POST <callbacks_base_path>/notification/callback`
- [ ] JWT middleware оборачивает оба эндпоинта (если verify_jwt)
- [ ] Без callbacks в конфиге — эндпоинты не регистрируются

```
go build ./... && go test ./internal/server/ -run TestServerWithCallbacks -v
```

### 6.3 Интеграция в serve-команду

- [ ] В `internal/cmd/serve.go` — чтение `cfg.Server.Callbacks`
- [ ] Создание handlers по правилам (ExecHandler / WebhookHandler)
- [ ] Сборка CallbackRouter, передача через `WithCallbacks()`
- [ ] Лог: количество правил, base_path, verify_jwt

```
go build ./... && go run . serve --help
```

### 6.4 Bot secret lookup для JWT

- [ ] Функция `botSecretLookup` в serve.go — ищет secret по bot_id
  среди сконфигурированных ботов
- [ ] Single-bot: один secret; multi-bot: lookup по bot_id
- [ ] Ошибка если bot_id неизвестен

```
go test ./internal/cmd/ -run TestBotSecretLookup -v
```

---

## Section 7: Надёжность

### 7.1 Graceful shutdown для async-обработчиков

- [ ] `sync.WaitGroup` в Server для отслеживания запущенных goroutine
- [ ] При shutdown — ожидание завершения (с timeout из `shutdownCtx`)
- [ ] Context cancellation для running handlers

```
go test ./internal/server/ -run TestGracefulShutdown -v
```

### 7.2 Panic recovery в async-обработчиках

- [ ] `recover()` в goroutine → лог + error tracker
- [ ] Паника не роняет сервер

```
go test ./internal/server/ -run TestAsyncPanicRecovery -v
```

---

## Section 8: Документация и спецификация

### 8.1 OpenAPI-спецификация

- [ ] Добавить в `internal/server/api/openapi.yaml`:
  `POST /command` — request body (CallbackPayload), response 202
- [ ] Добавить `POST /notification/callback` — request body, response 200
- [ ] Схемы: CallbackPayload, NotificationCallbackPayload

```
# Визуально: запуск serve с docs, открыть /docs/
go run . serve
```

### 8.2 Документация конфига

- [ ] Обновить `docs/configuration.md` — секция `server.callbacks`
  с описанием всех полей и примерами
- [ ] Обновить `docs/integrations.md` — описание callback-ов

```
# Ручная проверка: прочитать docs/, убедиться в полноте
```

### 8.3 Примеры

- [ ] Добавить закомментированный пример `callbacks` в `config-local.yaml`
- [ ] Добавить пример exec-скрипта в `examples/callback-handler.sh`

```
# Проверка парсинга конфига с раскомментированным примером
yq '.server.callbacks' config-local.yaml
```

---

## Порядок зависимостей

```
Section 1 (конфиг, типы, интерфейс)
  ├─→ Section 2 (exec + webhook handlers)
  ├─→ Section 3 (router)
  └─→ Section 4 (JWT)
        ↓
Section 5 (HTTP-эндпоинты)
        ↓
Section 6 (интеграция в server + serve)
        ↓
Section 7 (graceful shutdown, panic recovery)
        ↓
Section 8 (OpenAPI, docs, примеры)
```

Секции 2, 3, 4 можно делать параллельно.

---

## Ключевые принципы

1. **Обратная совместимость** — без `server.callbacks` всё работает как раньше
2. **Расширяемость** — интерфейс `CallbackHandler` для custom-обработчиков
3. **Отключаемость** — нет правил = 202 без действий
4. **Безопасность** — JWT-верификация по умолчанию включена
