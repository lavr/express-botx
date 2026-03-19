# RFC-013: Привязка бота к чату в конфигурации

- **Статус:** Draft
- **Дата:** 2026-03-14

## Контекст

Сейчас `chats` — плоский словарь `имя → UUID`:

```yaml
bots:
  deploy-bot:
    host: express.company.ru
    id: aaa-...
    secret: secret-deploy
  alert-bot:
    host: express.company.ru
    id: bbb-...
    secret: secret-alert

chats:
  deploy: 7ee8aaa9-...
  alerts: a1b2c3d4-...
```

При нескольких ботах пользователь обязан указывать `--bot` (CLI) или `"bot"` (HTTP API) для каждого вызова, даже если в организации чат `deploy` всегда обслуживается ботом `deploy-bot`, а `alerts` — ботом `alert-bot`. Это неудобно и создаёт ошибки.

## Предложение

Расширить формат `chats`: значение может быть как строкой (UUID), так и объектом с полями `id` (UUID) и `bot` (имя бота). Если для чата указан `bot`, он используется по умолчанию — но может быть переопределён явным `--bot` / `"bot"` в запросе.

### Формат конфигурации

```yaml
bots:
  deploy-bot:
    host: express.company.ru
    id: aaa-...
    secret: secret-deploy
  alert-bot:
    host: express.company.ru
    id: bbb-...
    secret: secret-alert

chats:
  # Короткая форма (обратная совместимость): только UUID
  general: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285

  # Расширенная форма: UUID + бот по умолчанию
  deploy:
    id: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285
    bot: deploy-bot
  alerts:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: alert-bot
```

### Использование

#### CLI `send`

```bash
# Бот берётся из конфига чата — не нужно указывать --bot
express-botx send --chat-id deploy "Деплой OK"
express-botx send --chat-id alerts "CPU > 90%"

# Явный --bot переопределяет бота из конфига чата
express-botx send --bot alert-bot --chat-id deploy "Срочно!"

# Чат без привязки к боту — правила как раньше (1 бот = авто, >1 = ошибка)
express-botx send --chat-id general "Hello"
```

#### HTTP `serve /send`

```bash
# Бот берётся из конфига чата
curl -X POST /api/v1/send \
  -d '{"chat_id": "deploy", "message": "OK"}'

# Явный "bot" переопределяет
curl -X POST /api/v1/send \
  -d '{"bot": "alert-bot", "chat_id": "deploy", "message": "Срочно!"}'
```

#### HTTP `serve /alertmanager`, `/grafana`

```bash
# Бот берётся из default_chat_id чата
curl -X POST /api/v1/alertmanager  # default_chat_id: alerts → bot: alert-bot

# Явный ?bot= переопределяет
curl -X POST /api/v1/alertmanager?bot=deploy-bot
```

### Правила резолва бота

1. Если `--bot` / `"bot"` / `?bot=` указан явно → использовать его
2. Иначе если чат (по alias) имеет поле `bot` → использовать его
3. Иначе если в конфиге ровно один бот → использовать его
4. Иначе → ошибка `"bot is required, available: ..."`

### Валидация и `--fail-fast`

При загрузке конфига проверяется, что `bot` в каждом чате ссылается на существующий бот из `bots`. Поведение при невалидной ссылке зависит от режима:

**CLI `send`** — всегда ошибка (команда одноразовая, нет смысла в отложенной валидации):

```
error: chat "deploy" references unknown bot "nonexistent", available: alert-bot, deploy-bot
```

**`serve --fail-fast`** — ошибка при старте (то же сообщение), сервер не запускается.

**`serve` (без `--fail-fast`)** — warning в лог, сервер запускается. Ошибка возникает в рантайме при попытке отправить в чат с невалидным ботом:

```
# При старте (лог):
WARN: chat "deploy" references unknown bot "nonexistent", ignoring bot binding

# При запросе (400):
{"ok": false, "error": "bot is required, available: alert-bot, deploy-bot"}
```

Это согласуется с поведением `--fail-fast` для аутентификации: без флага сервер стартует даже если бот не авторизовался, с флагом — падает.

## Изменения

### `internal/config/config.go`

Тип `Chats` меняется с `map[string]string` на `map[string]ChatConfig`:

```go
type ChatConfig struct {
    ID  string `yaml:"id"`
    Bot string `yaml:"bot,omitempty"`
}
```

Для обратной совместимости YAML-парсинга реализуется `UnmarshalYAML`:

```go
func (c *ChatConfig) UnmarshalYAML(value *yaml.Node) error {
    // Строка → только UUID
    if value.Kind == yaml.ScalarNode {
        c.ID = value.Value
        return nil
    }
    // Объект → структура с id и bot
    type plain ChatConfig
    return value.Decode((*plain)(c))
}
```

Новые методы:

```go
// ResolveChatBot возвращает имя бота, привязанного к чату.
// Пустая строка — бот не привязан.
func (c *Config) ResolveChatBot(chatAlias string) string

// ValidateChatBots проверяет, что все bot-ссылки в chats указывают на существующие боты.
// Если strict=true (CLI send, serve --fail-fast) — возвращает ошибку.
// Если strict=false (serve без --fail-fast) — логирует warning и очищает невалидные ссылки.
func (c *Config) ValidateChatBots(strict bool) error
```

`ResolveChatID()` адаптируется для работы с `ChatConfig.ID` вместо строки.

### `internal/cmd/send.go`

После `ResolveChatID()` — если `--bot` не указан, пробуем `cfg.ResolveChatBot(chatAlias)`:

```go
if flags.Bot == "" {
    if botName := cfg.ResolveChatBot(originalChatID); botName != "" {
        flags.Bot = botName
    }
}
```

### `internal/cmd/serve.go`

`chatResolver` возвращает дополнительно имя бота. Или: `buildSendRequest` / обработчики проверяют бота из чата.

### `internal/server/server.go`

`ChatResolver` расширяется, чтобы возвращать и UUID, и бота:

```go
type ChatResolveResult struct {
    ChatID string
    Bot    string // из конфига чата, может быть пустым
}

type ChatResolver func(chatID string) (ChatResolveResult, error)
```

`resolveRequestBot` учитывает бота из чата:

```go
func (s *Server) resolveRequestBot(ctx context.Context, requestBot, chatBot string) (string, string) {
    // 1. Explicit request bot → use it
    // 2. Chat-bound bot → use it
    // 3. Single bot → use it (len(BotNames) == 1)
    // 4. Error
}
```

### `internal/server/handler_send.go`

После резолва чата — передать `chatBot` в `resolveRequestBot`.

### `internal/server/handler_alertmanager.go`, `handler_grafana.go`

Аналогично — после резолва `default_chat_id` / `?chat_id=` получить бота из чата.

### `internal/cmd/bot.go` (chats alias)

`chats alias set` расширяется:

```bash
# Как раньше — только UUID
express-botx chats alias set deploy 7ee8aaa9-...

# С привязкой к боту
express-botx chats alias set deploy 7ee8aaa9-... --bot deploy-bot
```

`chats alias list` показывает бота:

```
Chat aliases (3):
  alerts    a1b2c3d4-...  (bot: alert-bot)
  deploy    7ee8aaa9-...  (bot: deploy-bot)
  general   7ee8aaa9-...
```

## Пример полного конфига

```yaml
bots:
  deploy-bot:
    host: express.company.ru
    id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
    secret: secret-deploy
  alert-bot:
    host: express.company.ru
    id: 99887766-5544-3322-1100-aabbccddeeff
    secret: env:ALERT_SECRET

chats:
  deploy:
    id: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285
    bot: deploy-bot
  alerts:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: alert-bot
  general: f40f109f-3c15-577c-987a-55473e7390d1

cache:
  type: file

server:
  listen: ":8080"
  api_keys:
    - name: ci
      key: env:CI_API_KEY
  alertmanager:
    default_chat_id: alerts
  grafana:
    default_chat_id: alerts
```

С таким конфигом:

```bash
# Всё работает без --bot:
express-botx send --chat-id deploy "OK"       # → deploy-bot
express-botx send --chat-id alerts "CPU high"  # → alert-bot
express-botx send --chat-id general "Hello"    # → ошибка (2 бота, бот не привязан)
express-botx send --bot deploy-bot --chat-id general "Hello"  # → OK

curl /api/v1/send -d '{"chat_id":"deploy","message":"OK"}'  # → deploy-bot
curl /api/v1/alertmanager  # default_chat_id: alerts → alert-bot
```

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `ChatConfig` с `UnmarshalYAML` (строка или объект) | Средняя |
| P0 | `ResolveChatBot()`, `ValidateChatBots()` | Низкая |
| P0 | CLI `send`: резолв бота из чата | Низкая |
| P0 | HTTP `serve`: резолв бота из чата в `/send`, `/alertmanager`, `/grafana` | Средняя |
| P1 | `chats alias set --bot`, обновлённый `chats alias list` | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/config/config.go` — `ChatConfig`, `UnmarshalYAML`, `ResolveChatBot`, `ValidateChatBots` |
| MODIFY | `internal/config/config_test.go` — тесты на новый формат |
| MODIFY | `internal/cmd/send.go` — резолв бота из чата |
| MODIFY | `internal/cmd/serve.go` — chatResolver возвращает бота |
| MODIFY | `internal/server/server.go` — `ChatResolver` возвращает бота, `resolveRequestBot` учитывает chatBot |
| MODIFY | `internal/server/handler_send.go` — передача chatBot |
| MODIFY | `internal/server/handler_alertmanager.go` — передача chatBot |
| MODIFY | `internal/server/handler_grafana.go` — передача chatBot |
| MODIFY | `internal/server/server_test.go` — тесты |
| MODIFY | `internal/cmd/serve_integration_test.go` — интеграционные тесты |

## Проверка

1. Короткая форма `deploy: UUID` — работает как раньше
2. Расширенная форма `deploy: {id: UUID, bot: name}` — бот подставляется автоматически
3. `--bot` / `"bot"` / `?bot=` переопределяет бота из конфига чата
4. Чат без `bot` + несколько ботов → ошибка «bot is required»
5. Чат без `bot` + один бот → бот берётся автоматически
6. Невалидный `bot` в конфиге + CLI `send` → ошибка
7. Невалидный `bot` в конфиге + `serve --fail-fast` → ошибка при старте
8. Невалидный `bot` в конфиге + `serve` (без `--fail-fast`) → warning, сервер стартует, ошибка при запросе в этот чат
9. `chats alias set deploy UUID --bot deploy-bot` → сохраняется
10. `chats alias list` → показывает привязку
11. Alertmanager/Grafana с `default_chat_id: alerts` → бот из конфига чата
12. Bot-secret auth: бот из чата не позволяет обойти привязку сигнатуры
