# Конфигурация

Полный справочник по настройке express-botx.

## Приоритет загрузки

Параметры загружаются слоями, каждый следующий перекрывает предыдущий:

1. **YAML-файл** (`--config`, `EXPRESS_BOTX_CONFIG`, `./express-botx.yaml` или `<os.UserConfigDir>/express-botx/config.yaml`)

Автопоиск использует платформенный каталог из `os.UserConfigDir()`:

- Linux: `~/.config/express-botx/config.yaml`
- macOS: `~/Library/Application Support/express-botx/config.yaml`
- Windows: `%AppData%/express-botx/config.yaml`
2. **Переменные окружения**
3. **Флаги командной строки**

## Файл конфигурации

```yaml
bots:
  deploy-bot:
    host: express.company.ru              # или http://localhost:8080 для dev
    id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
    secret: my-bot-secret
  alert-bot:
    host: express.company.ru
    id: 99887766-5544-3322-1100-aabbccddeeff
    token: vault:secret/data/express#alert_token  # статический токен (альтернатива secret)

chats:
  # Короткая форма: только UUID
  chat1: 1a2b3c4d-5e6f-7890-abcd-ef1234567890
  chat2: 2a2b3c4d-6e6f-8890-bbcd-ff1234567890

  # С привязкой к боту: бот подставляется автоматически
  deploy:
    id: 2b3c4d5e-6f7a-8901-bcde-f12345678901
    bot: deploy-bot
  alerts:
    id: 3c4d5e6f-7a8b-9012-cdef-123456789012
    bot: alert-bot

  # Чат по умолчанию — используется когда --chat-id / chat_id не указан
  general:
    id: 4d5e6f7a-8b9c-0123-def0-234567890123
    default: true

cache:
  type: file                              # none | file | vault (по умолчанию: file)
  file_path: $TMPDIR/express-botx-token    # поддерживает переменные окружения
  ttl: 31536000                           # секунды (по умолчанию: 1 год)

server:
  listen: ":8080"
  base_path: /api/v1
  api_keys:
    - name: monitoring
      key: env:MONITORING_API_KEY
  alertmanager:
    default_chat_id: alerts               # UUID или алиас (опционально)
    error_severities: [critical, warning] # по умолчанию
  grafana:
    default_chat_id: alerts
    error_states: [alerting]              # по умолчанию
```

## Переменные окружения

| Переменная | Описание |
|---|---|
| `EXPRESS_BOTX_CONFIG` | Путь к файлу конфигурации |
| `EXPRESS_BOTX_HOST` | Хост сервера eXpress (или URL: `http://host:port`) |
| `EXPRESS_BOTX_BOT_ID` | UUID бота |
| `EXPRESS_BOTX_SECRET` | Секрет бота |
| `EXPRESS_BOTX_TOKEN` | Токен бота (альтернатива секрету) |
| `EXPRESS_BOTX_CACHE_TYPE` | Тип кэша: `none`, `file`, `vault` |
| `EXPRESS_BOTX_CACHE_FILE_PATH` | Путь к файлу кэша токенов |
| `EXPRESS_BOTX_CACHE_TTL` | TTL кэша в секундах |
| `EXPRESS_BOTX_SERVER_LISTEN` | Адрес для прослушивания (serve) |
| `EXPRESS_BOTX_SERVER_BASE_PATH` | Базовый путь (serve) |
| `EXPRESS_BOTX_SERVER_API_KEY` | API-ключ (serve) |
| `EXPRESS_BOTX_VERBOSE` | Уровень логирования: 1-3 |

## Аутентификация

Бот может аутентифицироваться двумя способами.

### Secret (динамический токен)

Приложение хранит `secret` и получает токен через BotX API при каждом запуске. При 401 — автоматический refresh.

```yaml
bots:
  mybot:
    host: express.company.ru
    id: 054af49e-...
    secret: my-secret  # или env:VAR, или vault:path#key
```

```bash
express-botx send --secret "my-secret" --host h --bot-id ID "Hello"
```

### Token (статический токен)

Приложение хранит готовый токен, без обращения к API. Токены eXpress бессрочные. При 401 — ошибка (refresh невозможен без secret).

```yaml
bots:
  mybot:
    host: express.company.ru
    id: 054af49e-...
    token: eyJhbGci...  # или env:VAR, или vault:path#key
```

```bash
express-botx send --token "TOKEN" --host h --bot-id ID "Hello"
```

### Обмен secret на token

По умолчанию `config bot add` обменивает secret на token через API и сохраняет **только token** (secure by default):

```bash
# Secret → token (secret не сохраняется)
express-botx config bot add --host h --bot-id ID --secret SECRET

# Сохранить secret как есть
express-botx config bot add --host h --bot-id ID --secret SECRET --save-secret
```

## Форматы значений секретов

`--secret`, `--token` и поля `secret`/`token` в конфиге поддерживают:

```bash
# Литеральное значение
express-botx send --secret "my-secret-key" "Hello"

# Из переменной окружения
express-botx send --token env:MY_TOKEN "Hello"

# Из HashiCorp Vault (KV v2)
express-botx send --secret "vault:secret/data/express#bot_secret" "Hello"
```

Для Vault необходимы переменные `VAULT_ADDR` и `VAULT_TOKEN`.

## Мульти-бот конфигурация

При нескольких ботах выбор бота определяется по приоритету:

1. Явный `--bot` (CLI) или `"bot"` (API) / `?bot=` (webhooks)
2. Привязка бота к чату (`chats.deploy.bot: deploy-bot`)
3. Единственный бот (авто-выбор)
4. Ошибка

```bash
# Бот из привязки чата — --bot не нужен
express-botx send --chat-id deploy "OK"

# Явный --bot переопределяет привязку
express-botx send --bot alert-bot --chat-id deploy "Срочно!"

# HTTP API — аналогично
curl /api/v1/send -d '{"chat_id":"deploy","message":"OK"}'
curl /api/v1/send -d '{"bot":"alert-bot","chat_id":"deploy","message":"!"}'
curl /api/v1/alertmanager?bot=deploy-bot
```

## Чат по умолчанию

Один чат можно пометить как `default: true`. Он будет использоваться когда `--chat-id` (CLI) или `chat_id` (API) не указан:

```bash
# Управление через CLI
express-botx config chat add --chat-id UUID --alias general --default
express-botx config chat set general UUID --default
express-botx config chat set general UUID --no-default   # снять пометку
express-botx config chat list                             # покажет (default)
```

Приоритет выбора чата в HTTP-сервере:
- `/send`: `chat_id` из запроса → чат по умолчанию → ошибка
- `/alertmanager`, `/grafana`: `?chat_id=` → `default_chat_id` из конфига вебхука → чат по умолчанию → единственный чат → ошибка

## Формат host

```yaml
bots:
  prod:
    host: express.company.ru       # → https://express.company.ru
  local:
    host: http://localhost:8080    # HTTP + порт
  staging:
    host: https://staging.company.ru:8443
```

## Кэширование токенов

По умолчанию токен кэшируется в файл `.express-botx-token-cache.json` в текущей директории (TTL — 1 год).

### Файловый кэш

```yaml
cache:
  type: file
  file_path: $TMPDIR/express-botx-token  # опционально, поддерживает env vars
  ttl: 31536000
```

### Vault кэш

```yaml
cache:
  type: vault
  vault_url: https://vault.example.com
  vault_path: secret/data/express-botx/tokens
  ttl: 31536000
```

### Отключение кэша

```bash
express-botx send --no-cache "Hello"
```

Или в конфиге: `cache.type: none`.

## Callback-ы от Express Platform

Секция `server.callbacks` позволяет принимать callback-и от сервера Express
(эндпоинты `POST /command` и `POST /notification/callback`) и маршрутизировать
их на внешние обработчики.

```yaml
server:
  callbacks:
    base_path: /botx            # отдельный base path (по умолчанию = server.base_path)
    verify_jwt: true            # JWT-верификация включена по умолчанию
    rules:
      - events: [chat_created, added_to_chat]
        async: false            # sync — ждём завершения перед ответом 202
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

### Поля `server.callbacks`

| Поле | Тип | По умолчанию | Описание |
|---|---|---|---|
| `base_path` | string | `server.base_path` | Базовый путь для callback-эндпоинтов |
| `verify_jwt` | bool | `true` | JWT-верификация запросов от Express |
| `rules` | list | `[]` | Список правил маршрутизации |

### Поля правила (`rules[]`)

| Поле | Тип | По умолчанию | Описание |
|---|---|---|---|
| `events` | list | (обязательное) | Список типов событий для обработки |
| `async` | bool | `false` | `true` — ответ 202 сразу, обработка в фоне |
| `handler` | object | (обязательное) | Конфигурация обработчика |

### Поля обработчика (`handler`)

| Поле | Тип | Описание |
|---|---|---|
| `type` | string | `exec` — запуск команды, `webhook` — HTTP POST |
| `command` | string | Путь к команде (для `type: exec`) |
| `url` | string | URL для отправки (для `type: webhook`) |
| `timeout` | string | Таймаут в формате Go duration (`10s`, `1m`) |

### Типы событий

На `POST /command`:

- `message` — обычное текстовое сообщение
- `chat_created`, `added_to_chat`, `user_joined_to_chat` — добавление в чат
- `deleted_from_chat`, `left_from_chat`, `chat_deleted_by_user` — удаление из чата
- `cts_login`, `cts_logout` — вход/выход пользователя
- `event_edit` — редактирование сообщения
- `smartapp_event` — событие SmartApp
- `internal_bot_notification` — внутреннее уведомление бота
- `conference_created`, `conference_deleted` — конференции
- `call_started`, `call_ended` — звонки
- `*` — catch-all, совпадает с любым событием

На `POST /notification/callback`:

- `notification_callback` — статус доставки уведомления

### Передача данных обработчикам

**exec:**
- Полный JSON callback-а передаётся через stdin
- Env-переменные: `EXPRESS_CALLBACK_EVENT`, `EXPRESS_CALLBACK_SYNC_ID`,
  `EXPRESS_CALLBACK_BOT_ID`, `EXPRESS_CALLBACK_CHAT_ID`, `EXPRESS_CALLBACK_USER_HUID`
- Exit code 0 = успех, != 0 = ошибка (логируется)

**webhook:**
- HTTP POST на указанный URL с оригинальным JSON в теле
- Headers: `Content-Type: application/json`, `X-Express-Event`, `X-Express-Sync-ID`
- Ожидаемый ответ: HTTP 2xx = успех

### JWT-верификация

По умолчанию включена. Express подписывает запросы JWT-токеном (HS256),
используя secret бота. Токен передаётся в заголовке `Authorization: Bearer <token>`.

Для отключения верификации (например, в dev-окружении):

```yaml
server:
  callbacks:
    verify_jwt: false
    rules: [...]
```

---

## Конфигурация очереди (async-режим)

Для `enqueue`, `serve --enqueue` и `worker` нужна секция `queue` и, в зависимости от роли, `producer`, `worker` и `catalog`:

### Producer (enqueue / serve --enqueue)

```yaml
queue:
  driver: kafka           # или rabbitmq
  url: broker:9092
  name: express-botx
  reply_queue: express-botx-replies

producer:
  routing_mode: mixed      # direct | catalog | mixed

catalog:
  queue_name: express-botx-catalog
  cache_file: /var/lib/express-botx/catalog.json
  max_age: 10m
```

### Worker

```yaml
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
  group: express-botx

worker:
  retry_count: 3
  retry_backoff: 1s
  shutdown_timeout: 30s
  health_listen: ":8081"

catalog:
  queue_name: express-botx-catalog
  publish: true
  publish_interval: 30s

bots:
  alerts:
    host: express.company.ru
    id: bot-uuid
    secret: env:ALERTS_SECRET
```

Producer не нужны `secret`, `token` и полный список ботов — он не аутентифицируется в BotX API.
