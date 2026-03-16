# RFC-017: Чат по умолчанию (default chat)

- **Статус:** Done
- **Дата:** 2026-03-16

## Контекст

Сейчас при нескольких чатах в конфиге, если `--chat-id` не указан, команда `send` возвращает ошибку:

```
error: multiple chats configured, specify one with --chat-id: alerts, deploy, reports
```

Аналогично HTTP-эндпоинт `/send` требует `chat_id` в каждом запросе.

Для Alertmanager/Grafana проблема решена через `default_chat_id` в секции вебхука, но для CLI `send` и HTTP `/send` нет способа задать чат по умолчанию глобально.

Типичный сценарий: у команды три чата (deploy, alerts, general), но 90% сообщений летят в `general`. Каждый раз писать `--chat-id general` неудобно.

## Предложение

Добавить в `ChatConfig` необязательное поле `default: true`. Если ни один чат не указан при отправке и ровно один чат помечен как `default` — использовать его. Если `default` не задан ни у одного чата — поведение остаётся прежним (ошибка при нескольких чатах, авто-выбор при одном).

### Формат конфигурации

```yaml
chats:
  deploy: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285

  alerts:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: alert-bot

  general:
    id: f40f109f-3c15-577c-987a-55473e7390d1
    default: true
```

Короткая форма (`deploy: UUID`) не поддерживает `default` — для этого нужна расширенная форма.

### Использование

#### CLI `send`

```bash
# Чат не указан → используется general (default: true)
express-botx send "Hello"

# Явный --chat-id переопределяет default
express-botx send --chat-id deploy "Деплой OK"
```

#### HTTP `/send`

```bash
# chat_id не указан → используется general
curl -X POST /api/v1/send -d '{"message": "Hello"}'

# Явный chat_id переопределяет
curl -X POST /api/v1/send -d '{"chat_id": "deploy", "message": "OK"}'
```

#### Alertmanager / Grafana

Не затрагиваются — у них свой механизм `default_chat_id` в секции вебхука, который имеет приоритет. Если `default_chat_id` не задан — fallback на `default` чат (вместо текущего fallback на единственный чат).

### Правила резолва чата

1. Если `--chat-id` / `"chat_id"` / `?chat_id=` указан явно → использовать его
2. Если в конфиге ровно один чат → использовать его (текущее поведение)
3. Если есть чат с `default: true` → использовать его
4. Ошибка: `"multiple chats configured, specify one with --chat-id: ..."`

### Валидация

При загрузке конфига проверяется, что `default: true` указан не более чем у одного чата. Если помечено несколько:

```
error: multiple chats marked as default: alerts, general — only one allowed
```

## Изменения

### `internal/config/config.go`

Расширяется `ChatConfig`:

```go
type ChatConfig struct {
    ID      string `yaml:"id"`
    Bot     string `yaml:"bot,omitempty"`
    Default bool   `yaml:"default,omitempty"`
}
```

`UnmarshalYAML` не требует изменений — `default` декодируется автоматически через `Decode`.

`MarshalYAML` расширяется: если `Bot == ""` и `Default == false` — короткая форма (UUID), иначе — объект.

```go
func (c ChatConfig) MarshalYAML() (any, error) {
    if c.Bot == "" && !c.Default {
        return c.ID, nil
    }
    type plain ChatConfig
    return (plain)(c), nil
}
```

Новый метод валидации:

```go
// ValidateDefaultChat checks that at most one chat is marked as default.
func (c *Config) ValidateDefaultChat() error {
    var defaults []string
    for name, chat := range c.Chats {
        if chat.Default {
            defaults = append(defaults, name)
        }
    }
    if len(defaults) > 1 {
        sort.Strings(defaults)
        return fmt.Errorf("multiple chats marked as default: %s — only one allowed",
            strings.Join(defaults, ", "))
    }
    return nil
}
```

Новый метод для получения default-чата:

```go
// DefaultChat returns the alias and config of the chat marked as default.
// Returns empty alias if no default is configured.
func (c *Config) DefaultChat() (alias string, chat ChatConfig) {
    for name, ch := range c.Chats {
        if ch.Default {
            return name, ch
        }
    }
    return "", ChatConfig{}
}
```

### `RequireChatIDWithBot()` — ключевое изменение

```go
func (c *Config) RequireChatIDWithBot() (botName string, err error) {
    botName, err = c.ResolveChatIDWithBot()
    if err != nil {
        return "", err
    }
    if c.ChatID != "" {
        return botName, nil
    }
    switch len(c.Chats) {
    case 0:
        return "", fmt.Errorf("chat is required: use --chat-id or configure aliases in config (chats section)")
    case 1:
        for _, chat := range c.Chats {
            c.ChatID = chat.ID
            return chat.Bot, nil
        }
    default:
        // NEW: check for default chat
        if alias, chat := c.DefaultChat(); alias != "" {
            c.ChatID = chat.ID
            return chat.Bot, nil
        }
        names := make([]string, 0, len(c.Chats))
        for k := range c.Chats {
            names = append(names, k)
        }
        sort.Strings(names)
        return "", fmt.Errorf("multiple chats configured, specify one with --chat-id: %s",
            strings.Join(names, ", "))
    }
    return "", nil
}
```

### `internal/cmd/send.go`

Без изменений — логика default-чата инкапсулирована в `RequireChatIDWithBot()`.

### `internal/server/handler_send.go`

`chat_id` в запросе становится необязательным. Если пустой — вызывается `DefaultChat()` на конфиге:

```go
if payload.ChatID == "" {
    if alias, _ := s.cfg.DefaultChat(); alias != "" {
        payload.ChatID = alias
    } else {
        return writeError(w, http.StatusBadRequest, "chat_id is required")
    }
}
```

### `internal/server/handler_alertmanager.go`, `handler_grafana.go`

Fallback-цепочка расширяется: `?chat_id=` → `default_chat_id` из конфига вебхука → `DefaultChat()` из глобального конфига → `FallbackChatID` (единственный чат) → ошибка.

### `internal/cmd/serve.go`

`ValidateDefaultChat()` вызывается при загрузке конфига — рядом с `ValidateChatBots()`.

### CLI `config chats add`

`config chats add` получает флаг `--default`:

```bash
express-botx config chats add general f40f109f-... --default
```

Если чат добавляется с `--default`, а другой чат уже помечен как default — ошибка:

```
error: chat "alerts" is already marked as default, remove its default flag first
```

### CLI `config chats list`

Отображение default-чата:

```
Chats (3):
  alerts    a1b2c3d4-...  (bot: alert-bot)
  deploy    7ee8aaa9-...
  general   f40f109f-...  (default)
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
  general:
    id: f40f109f-3c15-577c-987a-55473e7390d1
    default: true

server:
  listen: ":8080"
  alertmanager:
    default_chat_id: alerts
  grafana:
    default_chat_id: alerts
```

С таким конфигом:

```bash
# Default chat:
express-botx send "Hello"                        # → general (default), ошибка "bot is required" (2 бота, бот не привязан)
express-botx send --bot deploy-bot "Hello"        # → general (default) через deploy-bot

# Явный chat:
express-botx send --chat-id deploy "OK"           # → deploy через deploy-bot
express-botx send --chat-id alerts "CPU high"     # → alerts через alert-bot

# HTTP:
curl /api/v1/send -d '{"message": "Hi"}'                         # → general (default)
curl /api/v1/send -d '{"chat_id": "deploy", "message": "OK"}'    # → deploy

# Alertmanager — свой default_chat_id, не затрагивается:
curl /api/v1/alertmanager  # → alerts (из default_chat_id в конфиге alertmanager)
```

## Обратная совместимость

Полная. Если ни один чат не помечен `default: true`, поведение идентично текущему: один чат — авто-выбор, несколько — ошибка.

## План реализации

### Шаг 1. Модель данных — `internal/config/config.go`

- [x] Добавить поле `Default bool` в `ChatConfig`
- [x] Обновить `MarshalYAML` — использовать объектную форму если `Default == true`
- [x] Реализовать `ValidateDefaultChat()` — не более одного default
- [x] Реализовать `DefaultChat()` — возвращает alias и конфиг default-чата
- [x] Обновить `RequireChatIDWithBot()` — в ветке `default` (несколько чатов) перед ошибкой проверять `DefaultChat()`

### Шаг 2. Валидация при загрузке — `internal/config/config.go`

- [x] В `Load()` (CLI) — вызвать `ValidateDefaultChat()` после `ValidateChatBots(true)`
- [x] В `LoadForServe()` — вызвать `ValidateDefaultChat()` после `ValidateChatBots(strict)`

### Шаг 3. HTTP `/send` — `internal/server/handler_send.go`

- [x] Сделать `chat_id` в запросе необязательным
- [x] Если `chat_id` пустой — попробовать `DefaultChat()`, иначе ошибка 400

### Шаг 4. Alertmanager/Grafana fallback — `internal/server/handler_alertmanager.go`, `handler_grafana.go`

- [x] Расширить fallback-цепочку: после `default_chat_id` и перед `FallbackChatID` — попробовать `DefaultChat()`

### Шаг 5. CLI команды — `internal/cmd/config_chats.go`

- [x] `config chats add` — добавить флаг `--default`, проверка на конфликт с существующим default
- [x] `config chats list` — отображать метку `(default)`

### Шаг 6. Тесты — `internal/config/config_test.go`

- [x] YAML парсинг: `default: true` в объектной форме
- [x] YAML сериализация: объектная форма при `Default == true`
- [x] `ValidateDefaultChat()`: 0 default — OK, 1 — OK, 2 — ошибка
- [x] `DefaultChat()`: возвращает правильный чат / пустой при отсутствии
- [x] `RequireChatIDWithBot()`: несколько чатов + default → авто-выбор
- [x] `RequireChatIDWithBot()`: несколько чатов без default → ошибка (регрессия)

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `ChatConfig.Default` + `UnmarshalYAML`/`MarshalYAML` | Низкая |
| P0 | `ValidateDefaultChat()` | Низкая |
| P0 | `DefaultChat()` + интеграция в `RequireChatIDWithBot()` | Низкая |
| P0 | HTTP `/send` — `chat_id` необязателен при наличии default | Низкая |
| P1 | Alertmanager/Grafana fallback на `DefaultChat()` | Низкая |
| P1 | `config chats add --default`, отображение в `config chats list` | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/config/config.go` — `ChatConfig.Default`, `ValidateDefaultChat()`, `DefaultChat()`, обновлённый `RequireChatIDWithBot()`, `MarshalYAML` |
| MODIFY | `internal/config/config_test.go` — тесты |
| MODIFY | `internal/cmd/send.go` — без изменений (логика в config) |
| MODIFY | `internal/cmd/serve.go` — вызов `ValidateDefaultChat()` |
| MODIFY | `internal/server/handler_send.go` — `chat_id` необязателен |
| MODIFY | `internal/server/handler_alertmanager.go` — fallback на DefaultChat |
| MODIFY | `internal/server/handler_grafana.go` — fallback на DefaultChat |

## Проверка

1. Конфиг без `default` — поведение как раньше
2. Один чат с `default: true` — используется при отсутствии `--chat-id`
3. Явный `--chat-id` переопределяет default
4. Два чата с `default: true` — ошибка валидации
5. Один чат в конфиге (без `default`) — авто-выбор как раньше
6. HTTP `/send` без `chat_id` + default → OK
7. HTTP `/send` без `chat_id` без default → ошибка 400
8. Alertmanager с `default_chat_id` — приоритет у `default_chat_id`, не у global default
9. Alertmanager без `default_chat_id` + global default → fallback на default chat
10. `config chats add --default` при существующем default → ошибка
11. `config chats list` — показывает `(default)` метку
