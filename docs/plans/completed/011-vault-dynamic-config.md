# RFC-011: Динамическая конфигурация из Vault

- **Статус:** Draft
- **Дата:** 2026-03-14

## Контекст

Сейчас Vault используется только для резолва отдельных значений:

```yaml
bots:
  prod:
    host: express.company.ru
    id: "uuid"
    secret: vault:secret/data/bot#secret   # только значение secret из Vault
```

Структура конфигурации (какие боты, чаты и API-ключи существуют) хранится в YAML-файле. Добавление нового бота, чата или API-ключа требует изменения конфига и редеплоя приложения.

### Желаемое поведение

- Добавление/удаление ботов — `vault kv patch`, без изменения конфига
- Добавление/удаление чатов — аналогично
- Ротация API-ключей — аналогично
- Конфиг приложения статичен, меняется только содержимое Vault

## Варианты

### Вариант 1a: JSON-значение в одном ключе

В Vault хранится JSON-blob в одном ключе:

```bash
vault kv put secret/express-botx/bots data='{
  "prod": {"host": "express.company.ru", "id": "uuid", "secret": "secret123"},
  "staging": {"host": "staging.company.ru", "id": "uuid2", "secret": "secret456"}
}'
```

Конфиг:

```yaml
bots: vault:secret/data/express-botx/bots#data
```

**Плюсы:**
- Один ключ, одна операция чтения
- Произвольная вложенность

**Минусы:**
- Неудобно редактировать через Vault UI/CLI — нужно перезаписывать весь blob
- `vault kv patch` работает на уровне ключей секрета, не внутри JSON
- Нет гранулярного аудита — любое изменение = перезапись всего

### Вариант 1b: Плоские ключи с конвенцией (рекомендуемый)

Каждое значение — отдельный ключ в Vault KV v2. Для вложенных структур (боты) используется конвенция `{name}.{field}`:

```bash
# Боты
vault kv put secret/express-botx/bots \
  prod.host=express.company.ru \
  prod.id=uuid-1 \
  prod.secret=secret-1 \
  staging.host=staging.company.ru \
  staging.id=uuid-2 \
  staging.secret=secret-2

# Чаты (уже плоские: alias → UUID)
vault kv put secret/express-botx/chats \
  deploy=chat-uuid-1 \
  ops-alerts=chat-uuid-2 \
  dev=chat-uuid-3

# API-ключи (плоские: name → key)
vault kv put secret/express-botx/api-keys \
  alertmanager=key-abc \
  grafana=key-xyz \
  ci=key-123
```

Конфиг:

```yaml
bots: vault:secret/data/express-botx/bots
chats: vault:secret/data/express-botx/chats
server:
  api_keys: vault:secret/data/express-botx/api-keys
  alertmanager:
    default_chat_id: ops-alerts
  grafana:
    default_chat_id: ops-alerts
```

Добавление бота без редеплоя:

```bash
vault kv patch secret/express-botx/bots \
  newbot.host=express2.company.ru \
  newbot.id=uuid-3 \
  newbot.secret=secret-3
```

Удаление чата:

```bash
# Прочитать текущие, убрать ненужный, перезаписать
vault kv get -format=json secret/express-botx/chats | \
  jq '.data.data | del(.dev)' | \
  vault kv put secret/express-botx/chats -
```

**Формат хранения в Vault:**

| Vault path | Формат ключей | Пример |
|---|---|---|
| `secret/data/express-botx/bots` | `{name}.host`, `{name}.id`, `{name}.secret` | `prod.host=express.company.ru` |
| `secret/data/express-botx/chats` | `{alias}` → UUID | `deploy=chat-uuid-1` |
| `secret/data/express-botx/api-keys` | `{name}` → key | `alertmanager=key-abc` |

**Плюсы:**
- Каждое значение — отдельный ключ, удобно менять через UI/CLI
- `vault kv patch` добавляет ключи без перезаписи существующих
- Чаты и API-ключи — уже `map[string]string`, ложатся 1:1
- Гранулярный аудит-лог в Vault
- Знакомый паттерн для ops-команд

**Минусы:**
- Парсинг `name.field` для ботов (тривиальная логика)
- Удаление ключа требует перезаписи всего секрета (ограничение KV v2)

### Вариант 2: Полный конфиг в одном Vault-ключе

В Vault хранится весь YAML-конфиг:

```bash
vault kv put secret/express-botx/config yaml='
bots:
  prod:
    host: express.company.ru
    id: uuid
    secret: secret123
chats:
  deploy: chat-uuid
server:
  api_keys:
    - name: alertmanager
      key: key-abc
'
```

Конфиг приложения минимален — только указатель:

```yaml
# вся конфигурация из Vault
import: vault:secret/data/express-botx/config#yaml
```

**Плюсы:**
- Максимальная гибкость — вся конфигурация динамическая
- Не нужен конфиг-файл вообще

**Минусы:**
- Vault превращается в хранилище конфигов — не его основная роль
- Неудобно редактировать YAML через Vault CLI/UI
- Нет гранулярности — один blob

### Вариант 3: Массив Vault-ссылок

```yaml
bots:
  prod: vault:secret/data/express-botx/bot-prod
  staging: vault:secret/data/express-botx/bot-staging
```

Каждый бот — отдельный Vault-секрет с ключами `host`, `id`, `secret`.

**Плюсы:**
- Каждый бот изолирован — удобно давать разные ACL-политики
- Простая структура каждого секрета

**Минусы:**
- Добавление бота по-прежнему требует изменения конфига (новая строка в `bots:`)
- Не решает основную проблему

## Рекомендация

**Вариант 1b** — плоские ключи с конвенцией `{name}.{field}`.

Причины:
1. Решает основную проблему — боты, чаты, ключи добавляются без редеплоя
2. Удобен для ops — стандартные операции через `vault kv put/patch`
3. Минимальная сложность реализации — парсинг `map[string]string` → структуры
4. Чаты и API-ключи ложатся на формат Vault без трансформации

## Реализация (предварительно)

### Резолв секций

При загрузке конфига, если значение секции — строка вида `vault:path`:

1. Прочитать все ключи из Vault-секрета
2. Для `chats` — использовать как `map[string]string` напрямую
3. Для `api_keys` — использовать как `map[string]string` (`name` → `key`)
4. Для `bots` — разобрать `{name}.{field}` → `map[string]BotConfig`

### Кэширование и обновление

Два варианта (решить позже):
- **При старте** — прочитать один раз, как сейчас. Для обновления — рестарт/SIGHUP
- **Периодически** — poll Vault каждые N секунд. Сложнее, но позволяет обновлять без рестарта

На первом этапе — при старте. Периодическое обновление — отдельный RFC.

### Затрагиваемые файлы

| Файл | Описание |
|------|----------|
| `internal/config/config.go` | Детект `vault:` строки в секциях `bots`, `chats` |
| `internal/config/vault.go` | Резолв секций из Vault, парсинг плоских ключей |
| `internal/cmd/serve.go` | Резолв `server.api_keys` из Vault |

## Открытые вопросы

- Нужен ли hot-reload (SIGHUP / poll) или достаточно чтения при старте?
- Нужно ли поддерживать микс — часть ботов в конфиге, часть в Vault?
- Стоит ли поддерживать вариант 1a (JSON) как альтернативу для тех, кому не нравятся плоские ключи?
- Как обрабатывать ситуацию, когда Vault недоступен при старте — fail fast или fallback на конфиг?
