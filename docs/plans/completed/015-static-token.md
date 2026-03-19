# RFC-015: Статический токен (режим без bot secret)

- **Статус:** Draft
- **Дата:** 2026-03-15

## Контекст

Согласно документации eXpress BotX API, токен, полученный через `GET /api/v2/botx/bots/{bot_id}/token?signature={signature}`, **имеет неограниченный срок жизни** (docs/express-api/botx-api-endpoints.md).

Сейчас express-botx работает в одном режиме аутентификации:

1. Пользователь настраивает `bot.id` + `bot.secret`
2. При старте (или по первому запросу) приложение запрашивает токен у eXpress API
3. Токен кэшируется (file или vault) с TTL (по умолчанию 1 год)
4. При 401 — автоматический refresh

Это требует, чтобы:
- bot secret был доступен приложению (в конфиге, env, или vault)
- eXpress API был доступен в момент аутентификации

На практике часто проще получить токен один раз (через curl или UI администратора) и использовать его напрямую, без хранения secret.

## Предложение

Добавить второй режим аутентификации: пользователь передаёт готовый токен вместо secret. Приложение использует его напрямую, без обращения к `/token` эндпоинту.

### Формат конфигурации

```yaml
bots:
  # Режим 1: secret (текущий, без изменений)
  deploy-bot:
    host: express.company.ru
    id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
    secret: my-bot-secret

  # Режим 2: статический токен
  alert-bot:
    host: express.company.ru
    id: 99887766-5544-3322-1100-aabbccddeeff
    token: eyJhbGciOiJIUzI1NiIs...
```

`secret` и `token` — взаимоисключающие. Если указаны оба — ошибка при загрузке. Если не указан ни один — ошибка (как сейчас).

### CLI

```bash
# Режим secret (текущий)
express-botx send --host h --bot-id ID --secret SECRET --chat-id CHAT "Hello"

# Режим token
express-botx send --host h --bot-id ID --token TOKEN --chat-id CHAT "Hello"

# serve
express-botx serve --config config.yaml
```

Новый флаг `--token` добавляется в `globalFlags`, аналогично `--secret`.

### Переменные окружения

```bash
# Режим secret (текущий)
EXPRESS_BOTX_SECRET=my-secret

# Режим token
EXPRESS_BOTX_TOKEN=eyJhbGci...
```

### Правила резолва

Secret и token — взаимоисключающие. Конфликт определяется **по источнику**.

**Ошибка** — если оба указаны на одном уровне:
- YAML: `secret:` + `token:` у одного бота → ошибка при парсинге
- CLI: `--secret` + `--token` → ошибка при парсинге флагов
- Env: `EXPRESS_BOTX_SECRET` + `EXPRESS_BOTX_TOKEN` → ошибка после applyEnv

**Вытеснение** — высший уровень заменяет низший:
- Конфиг `secret:` + CLI `--token` → token (secret очищается)
- Конфиг `token:` + env `EXPRESS_BOTX_SECRET` → secret (token очищается)
- Конфиг `secret:` + env `EXPRESS_BOTX_TOKEN` → token (secret очищается)

```bash
# Конфиг с secret, но хочу один раз использовать token:
express-botx send --token TOKEN --chat-id deploy "test"
# → secret из конфига очищается, используется token из CLI

# Конфиг с token, но хочу использовать secret:
EXPRESS_BOTX_SECRET=xxx express-botx send --chat-id deploy "test"
# → token из конфига очищается, используется secret из env
```

### Поведение

| | secret (текущий) | token (новый) |
|---|---|---|
| Аутентификация при старте | Запрос к eXpress API | Не нужна |
| Кэширование | Да (file/vault) | Не нужно (токен уже есть) |
| Refresh при 401 | Автоматический | Невозможен — ошибка проброшена |
| `--fail-fast` | Падает если API недоступен | Не влияет (нет запроса к API) |
| Lazy auth (serve без `--fail-fast`) | Retry каждые 10 сек | Не нужен |
| `bot ping` | Запрашивает токен + проверяет API | Только проверяет API |

### Refresh при 401 в token-режиме

Если eXpress вернул 401 на запрос с статическим токеном, refresh невозможен — нет secret. Варианты:

1. **Пробросить ошибку** — пользователь получит `401 Unauthorized` и должен обновить токен вручную
2. Для serve: вернуть 502 с сообщением `"bot token expired or revoked, re-configure token"`

Это ожидаемое поведение: если токен невалиден, нужно его обновить вручную.

## Изменения

### `internal/config/config.go`

Новые поля:

```go
type BotConfig struct {
    Host    string `yaml:"host"`
    ID      string `yaml:"id"`
    Secret  string `yaml:"secret,omitempty"`
    Token   string `yaml:"token,omitempty"`   // pre-obtained token (alternative to secret)
    Timeout int    `yaml:"timeout,omitempty"`
}

type Flags struct {
    // ...existing fields...
    Token  string  // --token flag
}
```

Новое поле в runtime Config:

```go
type Config struct {
    // ...existing fields...
    BotToken  string `yaml:"-"` // resolved static token (runtime only)
}
```

`resolveBot()` копирует `Token` из `BotConfig` в `cfg.BotToken`.

**Валидация YAML-ботов** — отдельная функция `validateBotConfigs()`, вызывается в `Load()` и `LoadForServe()` сразу после парсинга YAML, **до** `resolveBot()`. Это покрывает и single-bot, и multi-bot пути:

```go
// validateBotConfigs checks that no bot has both secret and token.
func (c *Config) validateBotConfigs() error {
    for name, bot := range c.Bots {
        if bot.Secret != "" && bot.Token != "" {
            return fmt.Errorf("bot %q has both secret and token, use one", name)
        }
    }
    return nil
}
```

Вызов в `Load()` и `LoadForServe()`:
```go
// Layer 1: YAML file
// ...unmarshal...

// Validate bot configs before any resolution
if err := cfg.validateBotConfigs(); err != nil {
    return nil, err
}

// Layer 2: resolve bot...
```

`resolveBot()` по-прежнему копирует `Token` в `cfg.BotToken`:

```go
func (c *Config) resolveBot(botFlag string) error {
    // ...find bot...
    c.BotSecret = bot.Secret
    c.BotToken = bot.Token
    // ...
}
```

**Валидация на уровне CLI** — в `runSend`/`runServe`, до `config.Load`:

```go
if flags.Secret != "" && flags.Token != "" {
    return fmt.Errorf("--secret and --token are mutually exclusive")
}
```

**Валидация на уровне env + вытеснение** — `applyEnv` меняет сигнатуру на `applyEnv(cfg *Config) error`:

```go
func applyEnv(cfg *Config) error {
    envSecret := os.Getenv("EXPRESS_BOTX_SECRET")
    envToken := os.Getenv("EXPRESS_BOTX_TOKEN")

    // Конфликт на одном уровне — ошибка
    if envSecret != "" && envToken != "" {
        return fmt.Errorf("both EXPRESS_BOTX_SECRET and EXPRESS_BOTX_TOKEN are set, use one")
    }

    // Env вытесняет config
    if envSecret != "" {
        cfg.BotSecret = envSecret
        cfg.BotToken = ""  // env secret wins over config token
    }
    if envToken != "" {
        cfg.BotToken = envToken
        cfg.BotSecret = ""  // env token wins over config secret
    }

    // ...existing env vars (HOST, BOT_ID, CACHE_*, etc.)...
    return nil
}
```

Вызов в `Load()`/`LoadForServe()`:
```go
if err := applyEnv(cfg); err != nil {
    return nil, err
}
```

**Вытеснение на уровне flags** — в `applyFlags`:

```go
func applyFlags(cfg *Config, flags Flags) {
    // ...existing flags...
    if flags.Secret != "" {
        cfg.BotSecret = flags.Secret
        cfg.BotToken = ""  // flag secret wins over env/config token
    }
    if flags.Token != "" {
        cfg.BotToken = flags.Token
        cfg.BotSecret = ""  // flag token wins over env/config secret
    }
}
```

**Финальная валидация** — после всех слоёв:

```go
if cfg.BotSecret == "" && cfg.BotToken == "" {
    return fmt.Errorf("bot secret or token is required (--secret, --token, EXPRESS_BOTX_SECRET, EXPRESS_BOTX_TOKEN, or config file)")
}
```

Поддержка `env:VAR` и `vault:path#key` для token — через тот же `secret.Resolve()`.

### `internal/config/config.go` — multi-bot completeness

Все проверки "есть ли полные credentials" должны учитывать `BotToken` как альтернативу `BotSecret`:

```go
// hasCredentials returns true if the config has enough to authenticate.
func (c *Config) hasCredentials() bool {
    return c.Host != "" && c.BotID != "" && (c.BotSecret != "" || c.BotToken != "")
}
```

Затронутые места:

**`Load()`** — multi-bot error path:
```go
// Было: cfg.Host == "" || cfg.BotID == "" || cfg.BotSecret == ""
// Стало:
if !cfg.hasCredentials() {
    // try chat-bound bot, then error
}
```

**`LoadForServe()`** — multi-bot mode detection:
```go
// Было: cfg.Host == "" || cfg.BotID == "" || cfg.BotSecret == ""
// Стало:
if !cfg.hasCredentials() {
    cfg.multiBot = true
}
```

**`clearStaleBotName()`** — проверяет совпадение **всех** effective credentials с named bot:
```go
func (c *Config) clearStaleBotName() {
    if c.BotName == "" { return }
    bot, ok := c.Bots[c.BotName]
    if !ok { return }
    // Всегда проверяем host и id — они должны совпадать в любом режиме.
    // Плюс credential (secret или token) должен совпадать с тем, что в конфиге.
    if c.Host != bot.Host || c.BotID != bot.ID {
        c.BotName = ""
        return
    }
    if bot.Secret != "" && c.BotSecret != bot.Secret {
        c.BotName = ""
    } else if bot.Token != "" && c.BotToken != bot.Token {
        c.BotName = ""
    }
}
```

### `internal/config/config.go` — ApplyChatBot и resolveBot

**`ApplyChatBot()`** — должен копировать Token:
```go
func (c *Config) ApplyChatBot(botName string) error {
    bot := c.Bots[botName]
    c.Host = bot.Host
    c.BotID = bot.ID
    c.BotSecret = bot.Secret
    c.BotToken = bot.Token  // NEW
    c.BotName = botName
    c.BotTimeout = bot.Timeout
    return nil
}
```

**`resolveBot()`** — аналогично:
```go
c.BotToken = bot.Token  // NEW (добавить во все ветки)
```

### `internal/cmd/serve.go` — multi-bot botCfg

При создании per-bot конфигов в multi-bot serve, копировать Token:
```go
for _, name := range botNames {
    bot := cfg.Bots[name]
    botCfg := *cfg
    botCfg.Host = bot.Host
    botCfg.BotID = bot.ID
    botCfg.BotSecret = bot.Secret
    botCfg.BotToken = bot.Token  // NEW
    botCfg.BotName = name
    // ...
}
```

### `internal/cmd/cmd.go`

```go
func globalFlags(fs *flag.FlagSet, flags *config.Flags) {
    // ...existing...
    fs.StringVar(&flags.Token, "token", "", "bot token (alternative to --secret)")
}
```

### `internal/cmd/cmd.go` — authenticate()

```go
func authenticate(cfg *config.Config) (string, token.Cache, error) {
    // Static token mode: return token directly, no cache needed
    if cfg.BotToken != "" {
        resolved, err := secret.Resolve(cfg.BotToken)
        if err != nil {
            return "", nil, fmt.Errorf("resolving token: %w", err)
        }
        return resolved, token.NoopCache{}, nil
    }

    // Secret mode: existing logic
    // ...
}
```

### `internal/cmd/serve.go` — botSender

В token-режиме `refreshToken` невозможен:

```go
func (s *botSender) Send(ctx context.Context, p *server.SendPayload) (string, error) {
    // ...existing send logic...
    if errors.Is(err, botapi.ErrUnauthorized) {
        if s.cfg.BotToken != "" {
            // Static token — cannot refresh
            return "", fmt.Errorf("bot token rejected (401), re-configure token for bot %q", s.cfg.BotName)
        }
        // Secret mode — refresh as before
        newTok, refreshErr := refreshToken(s.cfg, s.cache)
        // ...
    }
}
```

### `internal/server/auth.go` — bot secret auth

Token-only боты **должны быть исключены** из `allow_bot_secret_auth`. Если бот не имеет secret, `BuildSignature(botID, "")` создаст валидную (но тривиальную) signature — это дыра в безопасности.

В `serve.go`, при сборке `BotSignatures`, пропускать ботов без secret:

```go
if cfg.Server.AllowBotSecretAuth {
    srvCfg.BotSignatures = make(map[string]string)
    if cfg.IsMultiBot() {
        for name, bot := range cfg.Bots {
            if bot.Secret == "" {
                continue  // token-only bot — skip
            }
            secretKey, err := secret.Resolve(bot.Secret)
            // ...
            srvCfg.BotSignatures[auth.BuildSignature(bot.ID, secretKey)] = name
        }
    } else if cfg.BotSecret != "" {
        // Single-bot with secret
        // ...existing...
    }
    // If no signatures collected, disable bot secret auth
    if len(srvCfg.BotSignatures) == 0 {
        srvCfg.AllowBotSecretAuth = false
    }
}
```

### `internal/cmd/send.go` — 401 retry

Текущий код в `runSend` при 401 вызывает `refreshToken()`, который использует secret. В token-режиме secret нет — нужно сообщить об ошибке:

```go
if errors.Is(err, botapi.ErrUnauthorized) {
    if cfg.BotToken != "" {
        return fmt.Errorf("bot token rejected (401), re-configure token")
    }
    tok, err = refreshToken(cfg, cache)
    // ...existing retry...
}
```

### `internal/cmd/bot.go` — bot add

Новые флаги `--token` и `--save-secret`:

```bash
# С secret (по умолчанию) — обменивает на token, secret НЕ сохраняется
express-botx bot add mybot --host h --bot-id ID --secret SECRET

# С secret + --save-secret — сохраняет secret в конфиг (текущее поведение)
express-botx bot add mybot --host h --bot-id ID --secret SECRET --save-secret

# С готовым token — сохраняет как есть, без обращения к API
express-botx bot add mybot --host h --bot-id ID --token TOKEN
```

Валидация:
- Один из `--secret` или `--token` обязателен
- `--save-secret` без `--secret` — ошибка
- `--secret` + `--token` — ошибка

Логика при `--secret` (без `--save-secret`):

```go
// 1. Resolve secret
secretKey, err := secret.Resolve(secretVal)
// 2. Build signature
signature := auth.BuildSignature(botID, secretKey)
// 3. Get token from eXpress API
tok, err := auth.GetToken(ctx, host, botID, signature)
// 4. Save token (not secret) in config
cfg.Bots[name] = config.BotConfig{Host: host, ID: botID, Token: tok}
```

Логика при `--secret --save-secret`:

```go
// Save secret as-is (current behavior)
cfg.Bots[name] = config.BotConfig{Host: host, ID: botID, Secret: secretVal}
```

Логика при `--token`:

```go
// Save token as-is, no API call
cfg.Bots[name] = config.BotConfig{Host: host, ID: botID, Token: tokenVal}
```

Вывод:
```
# --secret (default)
Bot added: mybot (express.company.ru, 054af49e-..., token obtained)

# --secret --save-secret
Bot added: mybot (express.company.ru, 054af49e-..., secret saved)

# --token
Bot added: mybot (express.company.ru, 054af49e-..., token saved)
```

### `internal/cmd/bot.go` — bot ping

В token-режиме пропускает получение токена, сразу проверяет API:

```go
func runBotPing(args []string, deps Deps) error {
    // ...
    if cfg.BotToken != "" {
        tok, err := secret.Resolve(cfg.BotToken)
        // skip auth.GetToken, go directly to API check
    } else {
        // existing secret flow
    }
}
```

## Безопасность

- Токен эквивалентен по чувствительности bot secret — хранится в тех же местах (конфиг, env, vault)
- `bot info` не выводит token (как не выводит secret)
- В логах token маскируется так же, как secret

## Пример полного конфига

```yaml
bots:
  # Secret mode: динамическая аутентификация
  deploy-bot:
    host: express.company.ru
    id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
    secret: env:DEPLOY_BOT_SECRET

  # Token mode: статический токен
  alert-bot:
    host: express.company.ru
    id: 99887766-5544-3322-1100-aabbccddeeff
    token: vault:secret/data/express#alert_bot_token

chats:
  deploy:
    id: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285
    bot: deploy-bot
  alerts:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: alert-bot
```

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `BotConfig.Token`, `Config.BotToken`, `Flags.Token` | Низкая |
| P0 | `--token` в globalFlags | Низкая |
| P0 | `EXPRESS_BOTX_TOKEN` env | Низкая |
| P0 | Валидация secret vs token (взаимоисключение) | Низкая |
| P0 | `authenticate()` — прямой возврат token без API-вызова | Низкая |
| P0 | `refreshToken` → ошибка в token-режиме | Низкая |
| P0 | `bot add` — exchange по умолчанию, `--save-secret`, `--token` | Низкая |
| P0 | `bot ping` в token-режиме | Низкая |
| P0 | Тесты | Средняя |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/config/config.go` — `BotConfig.Token`, `Config.BotToken`, `Flags.Token`, валидация, applyEnv, applyFlags |
| MODIFY | `internal/cmd/cmd.go` — `--token` в globalFlags, `authenticate()` для token-режима |
| MODIFY | `internal/cmd/serve.go` — botSender без refresh в token-режиме |
| MODIFY | `internal/cmd/bot.go` — `bot add --token`, `bot ping` в token-режиме |
| MODIFY | `internal/cmd/send.go` — 401 retry: ошибка вместо refresh в token-режиме |
| MODIFY | `README.md` — документация |
| MODIFY | `internal/config/config_test.go` — тесты |
| MODIFY | `internal/cmd/cmd_test.go` — тесты |
| MODIFY | `internal/cmd/serve_integration_test.go` — интеграционный тест |

## Проверка

### Базовые сценарии
1. Конфиг с `secret` — работает как раньше
2. Конфиг с `token` — работает без обращения к API `/token`
3. Конфиг с `secret` + `token` у одного бота — ошибка при загрузке
4. Конфиг без `secret` и `token` — ошибка
5. `--token TOKEN` из CLI — работает
6. `EXPRESS_BOTX_TOKEN=TOKEN` из env — работает
7. `token: vault:path#key` — резолвится через secret.Resolve

### 401 retry
8. 401 в token-режиме (serve) — 502 без retry, сообщение "re-configure token"
9. 401 в token-режиме (CLI send) — ошибка без retry
10. 401 в secret-режиме — refresh как раньше

### Вытеснение credentials между уровнями
11. Конфиг с `secret`, CLI `--token` — token вытесняет, secret очищается
12. Конфиг с `token`, CLI `--secret` — secret вытесняет, token очищается
13. Конфиг с `secret`, env `EXPRESS_BOTX_TOKEN` — token вытесняет
14. `--host H --bot-id ID --token T` при multi-bot конфиге — работает (не ошибка "specify --bot")

### Ошибки при конфликте на одном уровне
15. YAML `secret:` + `token:` у одного бота (single-bot) — ошибка при загрузке
16. YAML `secret:` + `token:` у одного бота (multi-bot serve) — ошибка при загрузке
17. CLI `--secret` + `--token` — ошибка
18. env `EXPRESS_BOTX_SECRET` + `EXPRESS_BOTX_TOKEN` — ошибка

### clearStaleBotName
18. Named token-bot + `--host override` → BotName сбрасывается (host не совпадает)
19. Named token-bot + `--bot-id override` → BotName сбрасывается (id не совпадает)
20. Named token-bot без overrides → BotName сохраняется

### Multi-bot
21. Multi-bot serve: один бот с secret, другой с token — оба работают
22. Chat-bound bot с token — `ApplyChatBot` копирует token
23. Multi-bot serve: botCfg копирует token при создании per-bot sender

### Безопасность
24. `allow_bot_secret_auth` — token-only боты исключены из BotSignatures
25. `BuildSignature(botID, "")` **не** попадает в BotSignatures

### bot add
26. `bot add --secret SECRET` — обменивает на token, сохраняет только token
27. `bot add --secret SECRET --save-secret` — сохраняет secret в конфиг
28. `bot add --token TOKEN` — сохраняет token как есть
29. `bot add --secret SECRET --token TOKEN` — ошибка
30. `bot add --save-secret` без `--secret` — ошибка

### bot ping
31. `bot ping --token TOKEN` — проверяет API без получения токена
