# RFC-016: CLI-команды для управления конфигом (chats add, server apikey)

- **Статус:** Draft
- **Дата:** 2026-03-16

## Контекст

Сейчас в express-botx есть команды для управления ботами (`bot add/rm/list`) и алиасами чатов (`chats alias set/list/rm`). Однако отсутствуют:

1. **Удобный способ добавить чат в конфиг** — `chats alias set` требует знать UUID чата заранее. На практике пользователь хочет выбрать чат из списка тех, в которых состоит бот, и добавить его по имени.

2. **Команды для управления API-ключами сервера** — `server.api_keys` можно настроить только через ручное редактирование YAML. Нет CLI-команд для добавления, просмотра или удаления ключей.

### Текущее состояние

**Чаты:**
- `chats list` — список чатов из API (требует аутентификации)
- `chats alias set <name> <uuid>` — ручное добавление алиаса в конфиг
- `chats alias list` — список алиасов из конфига
- `chats alias rm <name>` — удаление алиаса

**API-ключи:**
- Только через YAML:
  ```yaml
  server:
    api_keys:
      - name: my-key
        key: "literal-value"
      - name: env-key
        key: "env:MY_API_KEY"
  ```
- Или через env/CLI-флаг при запуске (`EXPRESS_BOTX_SERVER_API_KEY`, `--api-key`)

## Предложение

### 1. `chats add` — добавление чата из API в конфиг

Новая команда, которая получает список чатов через API, показывает их пользователю, и добавляет выбранный чат как алиас в конфиг.

```bash
# Поиск чата по имени и добавление в конфиг
express-botx chats add --name "Deploy Alerts"

# Поиск + указание алиаса
express-botx chats add --name "Deploy Alerts" --alias deploy

# Добавление по UUID (без обращения к API, аналог chats alias set)
express-botx chats add --chat-id UUID --alias deploy

# С привязкой к боту
express-botx chats add --name "Deploy Alerts" --alias deploy --bot deploy-bot
```

**Логика работы с `--name`:**

1. Аутентификация бота (как в `chats list`)
2. Запрос списка чатов через API (`client.ListChats`)
3. Фильтрация по `--name` (case-insensitive substring match)
4. Если найден ровно один чат — добавить
5. Если найдено несколько — вывести список и попросить уточнить (или передать `--chat-id`)
6. Если не найдено — ошибка

**Если `--alias` не указан** — используется slug из имени чата:
- `"Deploy Alerts"` → `deploy-alerts`
- `"CI/CD notifications"` → `ci-cd-notifications`
- Транслитерация кириллицы: `"Деплой алерты"` → `deploj-alerty`

**Если алиас уже существует** — обновить (с предупреждением).

### 2. `server apikey` — управление API-ключами сервера

Новая группа подкоманд для управления секцией `server.api_keys` в конфиге.

#### `server apikey list`

```bash
express-botx server apikey list
```

Вывод:
```
API keys (2):
  my-key        literal (32 chars)
  env-key       env:MY_API_KEY
```

Значения ключей **не выводятся** (только тип и маска). С `--show-keys` — полный вывод.

#### `server apikey add`

```bash
# Сгенерировать случайный ключ и добавить
express-botx server apikey add --name monitoring

# Добавить конкретное значение
express-botx server apikey add --name monitoring --key "my-secret-key"

# Ссылка на env-переменную
express-botx server apikey add --name monitoring --key "env:MONITORING_API_KEY"

# Ссылка на vault
express-botx server apikey add --name monitoring --key "vault:secret/data/express#api_key"
```

**Если `--key` не указан** — генерируется случайный ключ (32 байта hex, как в `generateAPIKey()`). Значение выводится в stdout, чтобы пользователь мог его скопировать.

**Если имя уже существует** — ошибка. Для обновления: `server apikey rm <name>` + `server apikey add`.

#### `server apikey rm`

```bash
express-botx server apikey rm monitoring
```

Удаляет ключ из конфига по имени.

### 3. `server` — корневая группа команд

Новая группа `server` для подкоманд управления серверной конфигурацией.

```
Usage: express-botx server <command> [options]

Commands:
  apikey  Manage server API keys (add, list, rm)
```

В будущем сюда можно добавить другие подкоманды (`server config show`, и т.д.).

## Изменения

### Новые файлы

| Действие | Файл |
|----------|------|
| CREATE | `internal/cmd/server_apikey.go` — команды `server apikey add/list/rm` |
| CREATE | `internal/cmd/server_apikey_test.go` — тесты |

### Модифицируемые файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/cmd/cmd.go` — добавить `server` в диспетчер команд |
| MODIFY | `internal/cmd/chats.go` — добавить `chats add` |
| MODIFY | `internal/cmd/chats_test.go` — тесты для `chats add` |
| MODIFY | `internal/config/config.go` — метод `SaveConfig` для `api_keys` (если нужны доработки) |

### `internal/cmd/cmd.go` — диспетчер

```go
case "server":
    return runServer(args[1:], deps)
```

### `internal/cmd/chats.go` — chats add

```go
case "add":
    return runChatsAdd(args[1:], deps)
```

```go
func runChatsAdd(args []string, deps Deps) error {
    fs := flag.NewFlagSet("chats add", flag.ContinueOnError)
    fs.SetOutput(deps.Stderr)
    var flags config.Flags
    var nameFilter, alias, botFlag string

    globalFlags(fs, &flags)
    fs.StringVar(&nameFilter, "name", "", "chat name to search for (substring match)")
    fs.StringVar(&alias, "alias", "", "alias name (auto-generated from chat name if omitted)")
    fs.StringVar(&botFlag, "bot", "", "default bot for this chat")
    fs.Usage = func() {
        fmt.Fprintf(deps.Stderr, "Usage: express-botx chats add [options]\n\n"+
            "Find a chat by name via API and add it as an alias to the config.\n\n"+
            "Options:\n")
        fs.PrintDefaults()
    }

    if err := fs.Parse(args); err != nil { ... }

    if nameFilter == "" && flags.ChatID == "" {
        return fmt.Errorf("--name or --chat-id is required")
    }

    // Если --chat-id указан напрямую — не нужен API-вызов
    if flags.ChatID != "" {
        if alias == "" {
            return fmt.Errorf("--alias is required with --chat-id")
        }
        // Загрузить минимальный конфиг, добавить алиас, сохранить
        cfg, err := config.LoadMinimal(flags)
        ...
        cfg.Chats[alias] = config.ChatConfig{ID: flags.ChatID, Bot: botFlag}
        return cfg.SaveConfig()
    }

    // Иначе — поиск через API
    cfg, err := config.Load(flags)
    ...
    tok, _, err := authenticate(cfg)
    ...
    client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
    chats, err := client.ListChats(context.Background())
    ...

    // Фильтрация
    var matched []botapi.ChatInfo
    for _, c := range chats {
        if strings.Contains(strings.ToLower(c.Name), strings.ToLower(nameFilter)) {
            matched = append(matched, c)
        }
    }

    switch len(matched) {
    case 0:
        return fmt.Errorf("no chats matching %q", nameFilter)
    case 1:
        // Добавить в конфиг
        chat := matched[0]
        if alias == "" {
            alias = slugify(chat.Name)
        }
        ...
    default:
        // Вывести список
        fmt.Fprintf(deps.Stderr, "Multiple chats match %q:\n", nameFilter)
        for _, c := range matched {
            fmt.Fprintf(deps.Stderr, "  %s  %s (%s)\n", c.GroupChatID, c.Name, c.ChatType)
        }
        return fmt.Errorf("multiple matches, use --chat-id to specify")
    }
}
```

### `internal/cmd/server_apikey.go`

```go
func runServer(args []string, deps Deps) error {
    if len(args) == 0 {
        printServerUsage(deps.Stderr)
        return fmt.Errorf("subcommand required: apikey")
    }
    switch args[0] {
    case "apikey":
        return runServerAPIKey(args[1:], deps)
    ...
    }
}

func runServerAPIKey(args []string, deps Deps) error {
    if len(args) == 0 { ... }
    switch args[0] {
    case "list":
        return runServerAPIKeyList(args[1:], deps)
    case "add":
        return runServerAPIKeyAdd(args[1:], deps)
    case "rm":
        return runServerAPIKeyRm(args[1:], deps)
    }
}
```

**`server apikey add`:**

```go
func runServerAPIKeyAdd(args []string, deps Deps) error {
    fs := flag.NewFlagSet("server apikey add", flag.ContinueOnError)
    var flags config.Flags
    var name, key string

    fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
    fs.StringVar(&name, "name", "", "key name (required)")
    fs.StringVar(&key, "key", "", "key value (generated if omitted)")

    if err := fs.Parse(args); err != nil { ... }
    if name == "" {
        return fmt.Errorf("--name is required")
    }

    cfg, err := config.LoadMinimal(flags)
    ...

    // Проверка дублей
    for _, k := range cfg.Server.APIKeys {
        if k.Name == name {
            return fmt.Errorf("API key %q already exists, remove it first with: server apikey rm %s", name, name)
        }
    }

    if key == "" {
        key, err = generateAPIKey()
        if err != nil { ... }
        fmt.Fprintf(deps.Stdout, "Generated key: %s\n", key)
    }

    cfg.Server.APIKeys = append(cfg.Server.APIKeys, config.APIKeyConfig{
        Name: name,
        Key:  key,
    })

    if err := cfg.SaveConfig(); err != nil { ... }

    fmt.Fprintf(deps.Stdout, "API key added: %s\n", name)
    return nil
}
```

**`server apikey list`:**

```go
func runServerAPIKeyList(args []string, deps Deps) error {
    ...
    cfg, err := config.LoadMinimal(flags)
    ...

    return printOutput(deps.Stdout, cfg.Format, func() {
        if len(cfg.Server.APIKeys) == 0 {
            fmt.Fprintln(deps.Stdout, "No API keys configured.")
            fmt.Fprintln(deps.Stdout, "Add one with: express-botx server apikey add --name NAME")
            return
        }
        fmt.Fprintf(deps.Stdout, "API keys (%d):\n", len(cfg.Server.APIKeys))
        for _, k := range cfg.Server.APIKeys {
            desc := describeKeySource(k.Key)
            fmt.Fprintf(deps.Stdout, "  %-20s %s\n", k.Name, desc)
        }
    }, cfg.Server.APIKeys)
}

// describeKeySource returns a human-readable description of the key source.
func describeKeySource(key string) string {
    if strings.HasPrefix(key, "env:") {
        return key // "env:MY_VAR"
    }
    if strings.HasPrefix(key, "vault:") {
        return key // "vault:path#field"
    }
    // Literal key — show length only
    return fmt.Sprintf("literal (%d chars)", len(key))
}
```

**`server apikey rm`:**

```go
func runServerAPIKeyRm(args []string, deps Deps) error {
    ...
    name := fs.Arg(0)

    cfg, err := config.LoadMinimal(flags)
    ...

    found := false
    keys := make([]config.APIKeyConfig, 0, len(cfg.Server.APIKeys))
    for _, k := range cfg.Server.APIKeys {
        if k.Name == name {
            found = true
            continue
        }
        keys = append(keys, k)
    }
    if !found {
        return fmt.Errorf("API key %q not found", name)
    }

    cfg.Server.APIKeys = keys
    if err := cfg.SaveConfig(); err != nil { ... }

    fmt.Fprintf(deps.Stdout, "API key removed: %s\n", name)
    return nil
}
```

## Вспомогательная функция slugify

```go
// slugify converts a chat name to a URL-friendly alias.
// "Deploy Alerts" → "deploy-alerts"
// "CI/CD notifications" → "ci-cd-notifications"
func slugify(name string) string {
    name = strings.ToLower(name)
    // Replace non-alphanumeric with hyphens
    var b strings.Builder
    for _, r := range name {
        if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
            b.WriteRune(r)
        } else {
            if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
                b.WriteByte('-')
            }
        }
    }
    return strings.Trim(b.String(), "-")
}
```

## Примеры использования

### Сценарий 1: Быстрая настройка нового окружения

```bash
# 1. Добавить бота
express-botx bot add --host express.company.ru --bot-id UUID --secret SECRET

# 2. Найти и добавить чат
express-botx chats add --name "Deploy Alerts" --alias deploy

# 3. Добавить API-ключ для мониторинга
express-botx server apikey add --name monitoring

# 4. Готово — можно отправлять
express-botx send --chat-id deploy "Hello from CLI"

# И поднять сервер
express-botx serve
```

### Сценарий 2: Настройка API-ключей для разных потребителей

```bash
# Ключ для Alertmanager
express-botx server apikey add --name alertmanager

# Ключ для Grafana через env-переменную
express-botx server apikey add --name grafana --key "env:GRAFANA_API_KEY"

# Ключ из Vault для CI/CD
express-botx server apikey add --name ci --key "vault:secret/data/express#ci_api_key"

# Посмотреть что настроено
express-botx server apikey list
# API keys (3):
#   alertmanager         literal (64 chars)
#   grafana              env:GRAFANA_API_KEY
#   ci                   vault:secret/data/express#ci_api_key
```

## Обновление help

### Главное меню

```
Usage: express-botx <command> [options]

Commands:
  send    Send a message to a chat
  serve   Start HTTP server
  chats   Manage chats (list, info, add, alias)
  bot     Manage bots (ping, info, token, list, add, rm)
  server  Manage server config (apikey)
  user    User operations (search)
```

### `chats` help

```
Usage: express-botx chats <command> [options]

Commands:
  list    List chats the bot is a member of
  info    Show detailed information about a chat
  add     Find a chat and add it to config
  alias   Manage chat aliases (set, list, rm)
```

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `server apikey add/list/rm` | Низкая |
| P0 | `server` в диспетчере команд | Низкая |
| P1 | `chats add --chat-id UUID --alias NAME` (без API) | Низкая |
| P1 | `chats add --name NAME` (с поиском через API) | Средняя |
| P2 | `slugify()` — автогенерация алиасов | Низкая |
| P2 | Тесты | Средняя |

## Проверка

### server apikey add
1. `server apikey add --name test` — генерирует ключ, сохраняет в конфиг, выводит значение
2. `server apikey add --name test --key "my-key"` — сохраняет литеральный ключ
3. `server apikey add --name test --key "env:MY_KEY"` — сохраняет ссылку на env
4. `server apikey add --name test --key "vault:path#key"` — сохраняет ссылку на vault
5. `server apikey add --name test` при существующем `test` — ошибка
6. `server apikey add` без `--name` — ошибка

### server apikey list
7. `server apikey list` при пустом списке — подсказка
8. `server apikey list` — выводит имена и типы без значений
9. `server apikey list --format json` — JSON-вывод

### server apikey rm
10. `server apikey rm test` — удаляет ключ
11. `server apikey rm nonexistent` — ошибка

### chats add
12. `chats add --name "Deploy"` — находит чат, добавляет с автоалиасом
13. `chats add --name "Deploy" --alias deploy` — находит чат, добавляет с указанным алиасом
14. `chats add --name "Deploy" --bot deploy-bot` — привязка к боту
15. `chats add --name "nonexistent"` — ошибка "no chats matching"
16. `chats add --name "common-prefix"` при нескольких совпадениях — список + ошибка
17. `chats add --chat-id UUID --alias deploy` — добавляет без API-вызова
18. `chats add` без `--name` и `--chat-id` — ошибка
19. `chats add --chat-id UUID` без `--alias` — ошибка
