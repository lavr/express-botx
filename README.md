# express-botx

CLI и HTTP-сервер для отправки сообщений в корпоративный мессенджер eXpress через BotX API.

Поддерживает вебхуки от Alertmanager и Grafana, а также асинхронную отправку через очередь (RabbitMQ / Kafka).

## Установка

### Homebrew (macOS / Linux)

```bash
brew install lavr/tap/express-botx
```

### Бинарник с GitHub

```bash
# Linux (amd64)
curl -sL https://github.com/lavr/express-botx/releases/latest/download/express-botx-linux-amd64.tar.gz | tar xz
sudo mv express-botx /usr/local/bin/

# macOS (Apple Silicon)
curl -sL https://github.com/lavr/express-botx/releases/latest/download/express-botx-darwin-arm64.tar.gz | tar xz
sudo mv express-botx /usr/local/bin/
```

Доступные архивы: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64` (.zip).

### Docker

```bash
docker pull lavr/express-botx
```

### Go

```bash
go install github.com/lavr/express-botx@latest
```

### Из исходников

```bash
git clone https://github.com/lavr/express-botx.git
cd express-botx
go build -o express-botx .

# С поддержкой RabbitMQ
go build -tags rabbitmq -o express-botx .

# С поддержкой Kafka
go build -tags kafka -o express-botx .

# С обоими драйверами
go build -tags "rabbitmq kafka" -o express-botx .
```

### Helm

```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx
```

Или из исходников:

```bash
helm install express-botx ./charts/express-botx -f my-values.yaml
```

Минимальный `values.yaml`:

```yaml
config:
  bots:
    prod:
      host: express.company.ru
      id: "bot-uuid"
      secret: "bot-secret"
  chats:
    project1-alerts:
      id: "chat-uuid1"
      bot: prod
    project2-alerts:
      id: "chat-uuid2"
      bot: prod
  server:
    listen: ":8080"
    base_path: /api/v1
    api_keys:
      - name: monitoring
        key: "api-key"
    alertmanager:
      default_chat_id: ops-alerts
    grafana:
      default_chat_id: ops-alerts

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: express-botx.company.ru
      paths:
        - path: /
          pathType: Prefix
  tls:
    - hosts:
        - express-botx.company.ru
      secretName: express-botx-tls
```

Конфиг монтируется из Kubernetes Secret (не ConfigMap), т.к. содержит bot secret и API-ключи. Для использования существующего секрета: `existingSecret: my-secret`.

Для async-режима с очередью используйте два отдельных Deployment:

```bash
# API-сервер (HTTP → очередь)
helm install api oci://ghcr.io/lavr/charts/express-botx -f values-api.yaml
# Worker (очередь → BotX API)
helm install worker oci://ghcr.io/lavr/charts/express-botx -f values-worker.yaml
```

Подробнее о `mode: serve-enqueue` и `mode: worker` — в [README чарта](charts/express-botx/README.md).

## Команды

| Команда | Описание |
|---|---|
| `send` | Отправить сообщение и/или файл в чат |
| `enqueue` | Положить сообщение в очередь для асинхронной отправки |
| `serve` | Запустить HTTP-сервер (API + вебхуки) |
| `serve --enqueue` | HTTP-сервер в асинхронном режиме (HTTP → очередь) |
| `worker` | Читать сообщения из очереди и отправлять в BotX API |
| `bot ping` | Проверить авторизацию и доступность API |
| `bot info` | Показать информацию о боте |
| `bot token` | Получить токен бота (для скриптов) |
| `chats list` | Показать список чатов бота |
| `chats info` | Показать детальную информацию о чате |
| `user search` | Найти пользователя по email, HUID или AD-логину |
| `config bot add\|rm\|list` | Управление ботами в конфиге |
| `config chat add\|set\|import\|rm\|list` | Управление алиасами чатов |
| `config apikey add\|rm\|list` | Управление API-ключами сервера |
| `config show` | Показать путь к конфигу и сводку |

## send — отправка сообщений

```bash
# Текст как аргумент
express-botx send "Сборка #42 прошла успешно"

# Из файла
express-botx send --body-from report.txt

# Из stdin
echo "Deploy OK" | express-botx send

# С файлом-вложением
express-botx send --file report.pdf "Отчёт за март"

# Файл из stdin
cat image.png | express-botx send --file - --file-name image.png

# Все параметры через флаги
express-botx send --host express.company.ru --bot-id UUID --secret KEY --chat-id UUID "Hello"
```

При успехе утилита завершается молча (exit 0). Ошибки выводятся в stderr (exit 1).

### Флаги send

```
--chat-id       UUID или алиас целевого чата (опционально при наличии default)
--body-from     прочитать сообщение из файла
--file          путь к файлу-вложению (или - для stdin)
--file-name     имя файла (обязательно при --file -)
--status        статус уведомления: ok или error (по умолчанию: ok)
--silent        без push-уведомления получателю
--stealth       стелс-режим (сообщение видно только боту)
--force-dnd     доставить даже при DND
--no-notify     не отправлять уведомление вообще
--metadata      произвольный JSON для notification.metadata
```

## enqueue — асинхронная отправка через очередь

Кладёт сообщение в очередь (RabbitMQ / Kafka) вместо прямой отправки в BotX API. Требует сборки с соответствующим build tag.

```bash
# Direct mode — по UUID бота и чата
express-botx enqueue --bot-id BOT-UUID --chat-id CHAT-UUID "Hello"

# Catalog mode — по алиасам из local catalog cache
express-botx enqueue --routing-mode catalog --bot alerts --chat-id deploy "Deploy OK"

# Mixed mode (default) — UUID если указаны, иначе алиасы
express-botx enqueue --chat-id deploy "Hello"

# Из файла / stdin (аналогично send)
express-botx enqueue --body-from report.txt
echo "OK" | express-botx enqueue --bot-id UUID --chat-id UUID

# С файлом-вложением
express-botx enqueue --file report.pdf --bot-id UUID --chat-id UUID "Отчёт"
```

При успехе выводит `request_id` (text) или `{"ok":true,"queued":true,"request_id":"..."}` (json).

### Флаги enqueue

```
--routing-mode   direct | catalog | mixed (по умолчанию: mixed)
--bot-id         UUID бота (direct routing)
--bot            алиас бота из catalog (catalog/mixed)
--chat-id        UUID или алиас чата
--body-from      прочитать сообщение из файла
--file           путь к файлу-вложению (или - для stdin)
--file-name      имя файла (обязательно при --file -)
--status         статус уведомления: ok или error (по умолчанию: ok)
--silent         без push-уведомления
--stealth        стелс-режим
--force-dnd      доставить при DND
--no-notify      без уведомления
--metadata       JSON для notification.metadata
```

### Режимы маршрутизации (routing modes)

| Режим | Описание |
|---|---|
| `direct` | Producer получает конкретные `--bot-id` и `--chat-id` (UUID) и публикует без проверки. Не нужен catalog. |
| `catalog` | Алиасы (`--bot`, `--chat-id` по имени) резолвятся через локальный snapshot каталога. |
| `mixed` | Если указаны UUID — работает как `direct`. Если алиасы — через catalog. Рекомендуемый default. |

## worker — обработка очереди

Читает сообщения из очереди, отправляет в BotX API, публикует результаты в reply queue.

```bash
# Запуск worker'а
express-botx worker --config config.yaml

# С health check HTTP-сервером
express-botx worker --config config.yaml --health-listen :8081

# Без публикации каталога
express-botx worker --config config.yaml --no-catalog-publish
```

По умолчанию worker публикует routing catalog в отдельную queue/topic, чтобы producer'ы могли резолвить алиасы.

### Флаги worker

```
--health-listen       адрес для health check сервера (например, :8081)
--no-catalog-publish  отключить публикацию каталога
```

### Health check

При `--health-listen` worker поднимает HTTP-сервер:

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/healthz` | 200 если consumer подключён к брокеру, 503 иначе |
| `GET` | `/readyz` | 200 когда worker готов принимать сообщения, 503 при startup/shutdown |

## serve — HTTP-сервер

Запускает HTTP-сервер с эндпоинтами для отправки сообщений и приёма вебхуков.

```bash
express-botx serve --config config.yaml
express-botx serve --config config.yaml --listen :9090
express-botx serve --config config.yaml --api-key env:MY_API_KEY
```

### Эндпоинты

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/healthz` | Проверка здоровья |
| `POST` | `{basePath}/send` | Отправка сообщения (JSON / multipart) |
| `POST` | `{basePath}/alertmanager` | Приём вебхуков от Alertmanager |
| `POST` | `{basePath}/grafana` | Приём вебхуков от Grafana |

Все `POST`-эндпоинты требуют авторизации: `Authorization: Bearer <key>` или `X-API-Key: <key>`.

### serve --enqueue (асинхронный режим)

Переводит HTTP `/send` в асинхронный режим: вместо прямой отправки публикует задание в очередь и возвращает `202 Accepted`.

```bash
express-botx serve --enqueue --config config.yaml
```

Ответ в async-режиме:

```json
{"ok": true, "queued": true, "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2"}
```

HTTP payload расширяется полями `routing_mode` и `bot_id` для direct routing:

```json
{"routing_mode": "direct", "bot_id": "bot-uuid", "chat_id": "chat-uuid", "message": "deploy ok"}
```

### Docker

```bash
# Отправить сообщение
docker run --rm lavr/express-botx send \
  --host express.company.ru --bot-id UUID --secret KEY \
  --chat-id UUID "Hello from Docker"

# С конфигом
docker run --rm -v ./config.yaml:/config.yaml lavr/express-botx \
  send --config /config.yaml --chat-id UUID "Hello"

# HTTP-сервер
docker run --rm -p 8080:8080 -v ./config.yaml:/config.yaml lavr/express-botx \
  serve --config /config.yaml

# Worker
docker run --rm -v ./config.yaml:/config.yaml lavr/express-botx \
  worker --config /config.yaml --health-listen :8081
```

Сборка Docker-образа с поддержкой очередей:

```bash
# С RabbitMQ
docker build --build-arg BUILD_TAGS="sentry rabbitmq" -t express-botx:rabbitmq .

# С Kafka
docker build --build-arg BUILD_TAGS="sentry kafka" -t express-botx:kafka .

# С обоими драйверами
docker build --build-arg BUILD_TAGS="sentry rabbitmq kafka" -t express-botx:full .
```

## Конфигурация

Параметры загружаются слоями, каждый следующий перекрывает предыдущий:

1. **YAML-файл** (`--config`, `EXPRESS_BOTX_CONFIG`, `./express-botx.yaml` или `~/.config/express-botx/config.yaml`)
2. **Переменные окружения**
3. **Флаги командной строки**

### Файл конфигурации

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

### Конфигурация очереди (async-режим)

Для `enqueue`, `serve --enqueue` и `worker` нужна секция `queue` и, в зависимости от роли, `producer`, `worker` и `catalog`:

```yaml
# Минимальный конфиг для producer (enqueue / serve --enqueue)
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

```yaml
# Минимальный конфиг для worker
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

### Мульти-бот конфигурация

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

### Чат по умолчанию

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

Формат `host`:

```yaml
bots:
  prod:
    host: express.company.ru       # → https://express.company.ru
  local:
    host: http://localhost:8080    # HTTP + порт
  staging:
    host: https://staging.company.ru:8443
```

По умолчанию кэш пишется в файл `.express-botx-token-cache.json` в текущей директории.

Путь к конфигу: `--config /path/to/config.yaml` или `EXPRESS_BOTX_CONFIG=/path/to/config.yaml`

### Переменные окружения

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

### Общие флаги

```
--host          хост сервера eXpress
--bot-id      ID бота (UUID)
--bot           имя бота из конфига
--secret        секрет бота (литерал, env:VAR или vault:path#key)
--token         токен бота (альтернатива --secret)
--config        путь к файлу конфигурации
--no-cache      отключить кэширование токена
--format        формат вывода: text или json (по умолчанию: text)
-v / -vv / -vvv уровень подробности логирования
```

## Аутентификация

Бот может аутентифицироваться двумя способами:

### Secret (динамический токен)

Приложение хранит `secret` и получает токен через BotX API при каждом запуске:

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

При 401 — автоматический refresh токена.

### Token (статический токен)

Приложение хранит готовый токен, без обращения к API:

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

Токены eXpress бессрочные. При 401 — ошибка (refresh невозможен без secret).

### `config bot add` — обмен secret на token

По умолчанию `config bot add` обменивает secret на token через API и сохраняет **только token** (secure by default):

```bash
# Secret → token (secret не сохраняется)
express-botx config bot add --host h --bot-id ID --secret SECRET

# Сохранить secret как есть
express-botx config bot add --host h --bot-id ID --secret SECRET --save-secret

# Готовый token
express-botx config bot add --host h --bot-id ID --token TOKEN
```

### `bot token` — получение токена для скриптов

```bash
# Из конфига (бот с secret)
express-botx bot token --bot prod

# С явными флагами
express-botx bot token --host h --bot-id ID --secret SECRET

# Использование в скриптах
TOKEN=$(express-botx bot token --bot prod)

# Если бот уже с token — просто выводит его
express-botx bot token --bot token-bot
```

### `config chat add` — добавление чата в конфиг

Находит чат по имени через API и добавляет как алиас в конфиг:

```bash
# Поиск чата по имени
express-botx config chat add --name "Deploy Alerts"

# С указанием алиаса
express-botx config chat add --name "Deploy Alerts" --alias deploy

# По UUID (без обращения к API)
express-botx config chat add --chat-id UUID --alias deploy

# С привязкой к боту
express-botx config chat add --name "Deploy Alerts" --alias deploy --bot deploy-bot

# С пометкой как чат по умолчанию
express-botx config chat add --chat-id UUID --alias general --default
```

При `--name` выполняется поиск по подстроке (case-insensitive). Если найдено несколько чатов — выводится список и предлагается уточнить через `--chat-id`. Если `--alias` не указан — генерируется из имени чата, включая кириллицу (`"Deploy Alerts"` → `deploy-alerts`, `"Веб-админы"` → `veb-adminy`).

### `config chat import` — массовый импорт чатов в конфиг

Импортирует все чаты, в которых состоит бот, в секцию `chats:` конфига. По умолчанию импортируются только `group_chat`.

```bash
# Базовый импорт
express-botx config chat import --config config-local.yaml

# Импорт только конференций
express-botx config chat import --config config-local.yaml --only-type voex_call

# Dry run
express-botx config chat import --config config-local.yaml --dry-run

# Импорт с префиксом и явной привязкой к боту
express-botx config chat import --config config-local.yaml --bot deploy-bot --prefix team-

# Bootstrap: импортировать чаты, затем проверить алиасы
express-botx config chat import --config config-local.yaml --only-type group_chat
express-botx config chat list --config config-local.yaml
```

Поддерживаются флаги:

```text
--dry-run
--only-type group_chat|voex_call
--prefix team-
--skip-existing
--overwrite
```

Поведение по умолчанию безопасное:
- если alias уже указывает на тот же UUID, чат пропускается
- если UUID уже есть под другим alias, чат пропускается
- если alias занят другим UUID, команда завершается ошибкой

`--skip-existing` превращает alias-конфликт в skip, а `--overwrite` переписывает конфликтующий alias новым UUID. Эти флаги взаимоисключающие.

### `config apikey` — управление API-ключами сервера

```bash
# Сгенерировать случайный ключ
express-botx config apikey add --name monitoring

# Добавить конкретное значение
express-botx config apikey add --name monitoring --key "my-secret-key"

# Ссылка на переменную окружения
express-botx config apikey add --name grafana --key "env:GRAFANA_API_KEY"

# Ссылка на Vault
express-botx config apikey add --name ci --key "vault:secret/data/express#ci_api_key"

# Посмотреть ключи (значения скрыты)
express-botx config apikey list

# Удалить ключ
express-botx config apikey rm monitoring
```

При `--key` без значения генерируется случайный ключ (64 hex-символа) и выводится в stdout.

### Форматы значений

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

## Тестирование

```bash
go test ./...
```
