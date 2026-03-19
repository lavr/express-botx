# RFC-003: Мульти-бот конфигурация

- **Статус:** Draft
- **Дата:** 2026-03-13

## Контекст

Сейчас конфигурация поддерживает ровно одного бота:

```yaml
host: express.invitro.ru
bot:
  id: 054af49e-...
  secret: my-secret
chats:
  deploy: 7ee8aaa9-...
```

На практике в организации несколько eXpress-серверов (прод, тест, филиалы) или несколько ботов на одном сервере (деплой-бот, алерт-бот, отчёт-бот). Сейчас для каждого нужен отдельный конфиг-файл и `--config`:

```bash
express-botx send message --config config-prod.yaml --chat-id deploy "OK"
express-botx send message --config config-test.yaml --chat-id deploy "OK"
```

Это неудобно: много файлов, легко перепутать, нельзя переключаться одним флагом.

## Предложение

Заменить одиночную секцию `host` + `bot` на словарь именованных ботов `bots`. Выбор бота — через флаг `--bot <name>`. Логика аналогична chat-алиасам: один бот — берётся автоматически, несколько — требует указания.

### Формат конфигурации

```yaml
bots:
  prod:
    host: express.company.ru
    id:   054af49e-5e18-4dca-ad73-4f96b6de63fa
    secret: my-prod-secret
  test:
    host: express-test.company.ru
    id:   1a2b3c4d-5e6f-7890-abcd-ef1234567890
    secret: my-test-secret
  alerts:
    host: express.company.ru
    id:   99887766-5544-3322-1100-aabbccddeeff
    secret: env:ALERTS_BOT_SECRET

chats:
  deploy: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285
  alerts: a1b2c3d4-e5f6-7890-abcd-ef1234567890

cache:
  type: file
  ttl: 3600
```

### Использование

```bash
# Один бот в конфиге — выбирается автоматически
express-botx send message --chat-id deploy "Hello"

# Несколько ботов — указать --bot
express-botx send message --bot prod --chat-id deploy "Деплой OK"
express-botx send message --bot test --chat-id deploy "Тест OK"
express-botx send message --bot alerts --chat-id alerts "CPU > 90%"

# bot info / bot ping с указанием бота
express-botx bot info --bot prod
express-botx bot ping --bot test

# Все боты
express-botx bot list
```

### Правила резолва `--bot`

1. Если `--bot` указан → искать в секции `bots`
2. Если не найден → ошибка с перечислением доступных имён
3. Если `--bot` не указан и в `bots` ровно один → использовать его
4. Если `--bot` не указан и в `bots` несколько → ошибка с перечислением
5. Флаги `--host`, `--bot-id`, `--secret` по-прежнему перекрывают значения из конфига (для разовых вызовов без файла)
6. Переменные `EXPRESS_HOST`, `EXPRESS_BOT_ID`, `EXPRESS_SECRET` работают как фолбэк, если бот не выбран из конфига

### Пример ошибок

```
error: multiple bots configured, specify one with --bot: alerts, prod, test
error: unknown bot "staging", available: alerts, prod, test
```

## Кэш токенов

Сейчас ключ кэша — `bot_id`. При мульти-боте один и тот же бот (`id`) может быть на разных хостах (прод/тест). Ключ кэша меняется на составной: `host:bot_id`.

```go
cacheKey := cfg.Host + ":" + cfg.Bot.ID
```

Это гарантирует, что токен для `prod:054af49e-...` не будет использован для `test:054af49e-...`.

## CRUD-команды для ботов

Аналогично `chats alias set/list/rm` — управление ботами из CLI без ручного редактирования YAML:

```bash
# Добавить / обновить бота
express-botx bot add prod --host express.company.ru --bot-id 054af49e-... --secret my-secret

# Список ботов
express-botx bot list

# Удалить бота
express-botx bot rm test
```

`bot add` и `bot rm` используют `LoadMinimal()` + `SaveConfig()` — не требуют рабочей авторизации.

## Изменения

### `internal/config/config.go`

Новая структура:

```go
type Config struct {
    Bots   map[string]BotConfig `yaml:"bots,omitempty"`
    Chats  map[string]string    `yaml:"chats,omitempty"`
    Cache  CacheConfig          `yaml:"cache"`

    // Resolved at runtime (not persisted)
    Host     string `yaml:"-"`
    BotID    string `yaml:"-"`
    Secret   string `yaml:"-"`
    BotName  string `yaml:"-"` // resolved bot alias name
    ChatID   string `yaml:"-"`
    Format   string `yaml:"-"`
    configPath string
}

type BotConfig struct {
    Host   string `yaml:"host"`
    ID     string `yaml:"id"`
    Secret string `yaml:"secret"`
}
```

Новый флаг в `Flags`:

```go
type Flags struct {
    ConfigPath string
    Bot        string  // --bot name
    Host       string
    BotID      string
    Secret     string
    ChatID     string
    NoCache    bool
    Format     string
}
```

Логика `Load()`:

1. Читает YAML, env, flags
2. `resolveBot()`: если `flags.Bot != ""` → ищет в `Bots`; иначе если `len(Bots) == 1` → берёт единственный; иначе если `len(Bots) > 1` → ошибка
3. Из выбранного `BotConfig` заполняет `cfg.Host`, `cfg.BotID`, `cfg.Secret` (runtime-поля)
4. CLI-флаги `--host`, `--bot-id`, `--secret` перекрывают значения из бота
5. Валидация: host, bot_id, secret обязательны

`RequireBot() error` — аналог `RequireChatID()`, вызывается в `Load()`.

Ключ кэша:

```go
cacheKey := cfg.Host + ":" + cfg.Bot.ID
```

### `internal/cmd/cmd.go`

В `globalFlags()`:

```go
fs.StringVar(&flags.Bot, "bot", "", "bot name from config")
```

### `internal/cmd/bot.go`

Новые субкоманды:

**`bot list`** — список ботов из конфига (использует `LoadMinimal`, не требует авторизации):

```bash
express-botx bot list
express-botx bot list --format json
```

Текстовый вывод:

```
Bots (3):
  prod      express.company.ru      054af49e-...
  test      express-test.company.ru 1a2b3c4d-...
  alerts    express.company.ru      99887766-...
```

**`bot add`** — добавить/обновить бота в конфиге (использует `LoadMinimal` + `SaveConfig`):

```bash
express-botx bot add prod --host express.company.ru --bot-id 054af49e-... --secret my-secret
express-botx bot add test --host express-test.company.ru --bot-id 1a2b3c4d-... --secret env:TEST_SECRET
```

Флаги `--host`, `--bot-id`, `--secret` обязательны. Позиционный аргумент — имя бота.

**`bot rm`** — удалить бота из конфига:

```bash
express-botx bot rm test
```

Ошибка, если бот не найден.

**`bot info`** — показывает инфо о выбранном (или единственном) боте. Поле `BotName` добавляется в вывод.

### Остальные команды

Без изменений — все работают через `cfg.Host`, `cfg.BotID`, `cfg.Secret`, которые заполняются при резолве бота.

### Переменные окружения

`EXPRESS_HOST`, `EXPRESS_BOT_ID`, `EXPRESS_SECRET` — используются как фолбэк, когда бот не выбран из конфига. Порядок:

1. CLI-флаги (`--host`, `--bot-id`, `--secret`)
2. Выбранный бот из `bots` (по `--bot` или единственный)
3. Переменные окружения
4. Ошибка

## Миграция

Обратная совместимость не требуется. Старый формат (`host` + `bot.id` + `bot.secret` на верхнем уровне) перестаёт работать. Новый формат:

```yaml
# Было:
host: express.company.ru
bot:
  id: 054af49e-...
  secret: my-secret

# Стало:
bots:
  main:
    host: express.company.ru
    id: 054af49e-...
    secret: my-secret
```

Минимальный конфиг с одним ботом — один именованный бот, `--bot` не нужен.

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | Секция `bots` + резолв `--bot` в `Load()` | Средняя |
| P0 | Составной ключ кэша `host:bot_id` | Низкая |
| P0 | Флаг `--bot` в `globalFlags()` | Низкая |
| P0 | `bot list` — список ботов из конфига | Низкая |
| P0 | `bot add` / `bot rm` — CRUD ботов | Низкая |
| P0 | Обновление `bot info` — имя бота в выводе | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/config/config.go` — `Bots map`, `BotConfig.Host`, `resolveBot()`, обновление `Load()` |
| MODIFY | `internal/cmd/cmd.go` — `--bot` в `globalFlags()`, составной `cacheKey` |
| MODIFY | `internal/cmd/bot.go` — `bot list`, `bot add`, `bot rm`, обновление `bot info` |
| MODIFY | `README.md` — документация |

## Проверка

1. Конфиг с одним ботом, без `--bot` — работает автоматически
2. Конфиг с несколькими ботами, без `--bot` — ошибка с перечислением
3. `--bot prod` — резолвится, все команды работают
4. `--bot unknown` — ошибка с перечислением
5. `--bot prod --host override.ru` — host перекрыт флагом
6. `bot list` — показывает все боты из конфига
7. `bot add prod --host ... --bot-id ... --secret ...` — добавляет бота
8. `bot rm test` — удаляет бота
9. `bot info --bot prod` — инфо конкретного бота
10. Без конфига, только `--host --bot-id --secret` — работает как раньше
11. Кэш: два бота с одинаковым `id` на разных хостах — токены не пересекаются
