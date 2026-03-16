# express-botx

CLI и HTTP-сервер для отправки сообщений в корпоративный мессенджер eXpress через BotX API.

Поддерживает вебхуки от Alertmanager и Grafana.

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

## Команды

| Команда | Описание |
|---|---|
| `send` | Отправить сообщение и/или файл в чат |
| `serve` | Запустить HTTP-сервер (API + вебхуки) |
| `bot ping` | Проверить авторизацию и доступность API |
| `bot info` | Показать информацию о боте |
| `bot token` | Получить токен бота (для скриптов) |
| `chats list` | Показать список чатов бота |
| `chats info` | Показать детальную информацию о чате |
| `user search` | Найти пользователя по email, HUID или AD-логину |
| `config bot add\|rm\|list` | Управление ботами в конфиге |
| `config chat add\|set\|rm\|list` | Управление алиасами чатов |
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

При `--name` выполняется поиск по подстроке (case-insensitive). Если найдено несколько чатов — выводится список и предлагается уточнить через `--chat-id`. Если `--alias` не указан — генерируется из имени чата (`"Deploy Alerts"` → `deploy-alerts`).

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
