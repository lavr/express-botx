# express-bot

CLI-утилита для работы с корпоративным мессенджером eXpress через BotX API.

Поддерживает отправку сообщений и файлов в чаты, получение информации о боте. Работает в стиле Unix: принимает текст из аргумента, файла или stdin, конфигурируется через файл, переменные окружения и флаги.

## Установка

```bash
go install github.com/lavr/express-bot@latest
```

Или сборка из исходников:

```bash
git clone https://github.com/lavr/express-bot.git
cd express-send
go build -o express-bot .
```

## Использование

```bash
express-bot <command> [options]
```

### Команды

| Команда | Описание |
|---|---|
| `send message` | Отправить текстовое сообщение в чат |
| `send file` | Отправить файл в чат |
| `chats list` | Показать список чатов бота |
| `chats info` | Показать детальную информацию о чате |
| `bot ping` | Проверить авторизацию и доступность API |
| `bot info` | Показать информацию о боте и статус авторизации |
| `user search` | Найти пользователя по email, HUID или AD-логину |

### send message — отправка сообщений

```bash
# Текст как аргумент
express-bot send message "Сборка #42 прошла успешно"

# Несколько слов без кавычек тоже работают
express-bot send message Deploy finished successfully

# Из файла
express-bot send message --from report.txt

# Из stdin (пайплайн)
echo "Deploy OK" | express-bot send message
cat changelog.md | express-bot send message

# Всё через флаги
express-bot send message --host express.company.ru --bot-id UUID --secret KEY --chat-id UUID "Hello"
```

При успехе утилита завершается молча (exit 0). Ошибки выводятся в stderr (exit 1).

### send file — отправка файлов

```bash
# Файл по пути
express-bot send file --chat-id UUID ./report.pdf

# С подписью
express-bot send file --chat-id UUID --caption "Отчёт за март" ./report.pdf

# Из stdin (--filename обязателен)
cat image.png | express-bot send file --chat-id UUID --filename image.png
```

### chats list — список чатов

```bash
express-bot chats list
express-bot chats list --config /path/to/config.yaml
```

Выводит список чатов, в которых состоит бот (имя, тип, количество участников).

### chats info — информация о чате

```bash
express-bot chats info --chat-id UUID
express-bot chats info --chat-id UUID --format json
```

Выводит детали чата: имя, тип, описание, shared_history, список участников.

### bot ping — проверка доступности

```bash
express-bot bot ping
express-bot bot ping --quiet   # только exit code
```

Проверяет авторизацию (получение токена) и доступность API (запрос списка чатов). Всегда обходит кэш токенов.

Формат вывода:
- Успех: `OK 234ms`
- Ошибка авторизации: `FAIL auth: ...`
- Ошибка API: `FAIL api: ...`

### bot info — информация о боте

```bash
express-bot bot info
express-bot bot info --format json
```

Показывает Bot ID, хост, режим кэширования и статус авторизации.

### user search — поиск пользователя

```bash
express-bot user search --email user@example.com
express-bot user search --huid UUID
express-bot user search --ad-login jdoe
express-bot user search --email user@example.com --format json
```

Ищет пользователя по одному из трёх параметров (обязательно указать ровно один). Выводит HUID, имя, email, AD-логин, отдел, должность.

## Формат вывода

По умолчанию вывод в текстовом формате. Флаг `--format json` переключает вывод в JSON с отступами:

```bash
express-bot chats list --format json
express-bot bot info --format json
express-bot user search --email user@example.com --format json
```

## Конфигурация

Параметры загружаются слоями, каждый следующий перекрывает предыдущий:

1. **YAML-файл** (`~/.config/express-send/config.yaml`)
2. **Переменные окружения**
3. **Флаги командной строки**

### Файл конфигурации

По умолчанию: `~/.config/express-send/config.yaml`

```yaml
host: express.company.ru
bot_id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
secret: my-bot-secret
chat_id: 1a2b3c4d-5e6f-7890-abcd-ef1234567890

cache:
  type: file                  # none | file | vault
  file_path: /tmp/tokens.json # только для type: file
  ttl: 3600                   # время жизни токена в секундах
```

Путь к файлу можно указать явно: `--config /path/to/config.yaml`

### Переменные окружения

| Переменная | Описание |
|---|---|
| `EXPRESS_HOST` | Хост сервера eXpress |
| `EXPRESS_BOT_ID` | UUID бота |
| `EXPRESS_SECRET` | Секрет бота |
| `EXPRESS_CHAT_ID` | UUID чата |
| `EXPRESS_CACHE_TYPE` | Тип кэша: `none`, `file`, `vault` |
| `EXPRESS_CACHE_FILE_PATH` | Путь к файлу кэша токенов |
| `EXPRESS_CACHE_VAULT_URL` | URL Vault-сервера |
| `EXPRESS_CACHE_VAULT_PATH` | Путь в Vault KV v2 |
| `EXPRESS_CACHE_TTL` | TTL кэша в секундах |

### Общие флаги

```
--host          хост сервера eXpress
--bot-id        UUID бота
--secret        секрет бота (литерал, env:VAR или vault:path#key)
--config        путь к файлу конфигурации
--no-cache      отключить кэширование токена
--format        формат вывода: text или json (по умолчанию: text)
```

### Флаги команды send message

```
--chat-id       UUID целевого чата
--from          прочитать сообщение из файла
```

### Флаги команды send file

```
--chat-id       UUID целевого чата
--caption       подпись к файлу
--filename      имя файла (обязательно при чтении из stdin)
```

### Флаги команды bot ping

```
--quiet         только exit code, без вывода
```

## Секреты

Значение `--secret` (и поле `secret` в конфиге) поддерживает три формата:

```bash
# Литеральное значение
express-bot send message --secret "my-secret-key" "Hello"

# Из переменной окружения
express-bot send message --secret env:EXPRESS_BOT_SECRET "Hello"

# Из HashiCorp Vault (KV v2)
express-bot send message --secret "vault:secret/data/express#bot_secret" "Hello"
```

Для Vault необходимы переменные `VAULT_ADDR` и `VAULT_TOKEN`.

## Кэширование токенов

Утилита получает токен бота при каждом запуске. Чтобы избежать лишних запросов, токен можно кэшировать.

### Файловый кэш

```yaml
cache:
  type: file
  file_path: ~/.cache/express-send/tokens.json  # опционально, есть значение по умолчанию
  ttl: 3600
```

### Vault кэш

Хранит токены в HashiCorp Vault KV v2:

```yaml
cache:
  type: vault
  vault_url: https://vault.example.com
  vault_path: secret/data/express-send/tokens
  ttl: 3600
```

Требует переменную `VAULT_TOKEN`. Таймаут на операции с Vault — 5 секунд.

### Отключение кэша

```bash
express-bot send message --no-cache "Hello"
```

Или в конфиге: `cache.type: none` (значение по умолчанию).

## Как это работает

1. Загрузка конфигурации (YAML + env + флаги)
2. Чтение сообщения (файл / аргумент / stdin) — для `send message`
3. Разрешение секрета (литерал / env / Vault)
4. Подпись HMAC-SHA256: `HMAC(key=secret, msg=bot_id)` — hex uppercase
5. Получение токена: `GET /api/v2/botx/bots/{bot_id}/token?signature={sig}`
6. Выполнение команды:
   - `send message`: `POST /api/v4/botx/notifications/direct` с `Authorization: Bearer {token}`
   - `send file`: `POST /api/v3/botx/files/upload` (multipart/form-data)
   - `chats list`: `GET /api/v3/botx/chats/list`
   - `chats info`: `GET /api/v3/botx/chats/list` → фильтрация по UUID
   - `bot ping`: auth + `GET /api/v3/botx/chats/list` (обходит кэш)
   - `bot info`: конфигурация + проверка auth
   - `user search`: `POST /api/v3/botx/users/by_huid|by_email|by_login`
7. При ответе 401 — автоматический повтор с новым токеном (один раз, для `send message` и `send file`)

## Структура проекта

```
express-send/
  main.go                       # точка входа
  internal/
    cmd/                        # субкоманды
      cmd.go                    #   диспетчер, Deps, authenticate, helpers
      send.go                   #   send message
      sendfile.go               #   send file
      chats.go                  #   chats list, chats info
      bot.go                    #   bot ping, bot info
      user.go                   #   user search
      output.go                 #   printOutput() — text/json formatter
    botapi/client.go            # API-клиент: ListChats(), GetChatInfo(), SendNotification(), UploadFile(), SearchUserBy*()
    config/config.go            # Config struct, Load() — YAML/env/flag layering
    secret/secret.go            # Resolve(): литерал / env:VAR / vault:path#key
    auth/auth.go                # BuildSignature(), GetToken()
    token/                      # кэширование токенов
      cache.go                  #   интерфейс Cache + NoopCache
      file.go                   #   файловый кэш
      vault.go                  #   Vault KV v2 кэш
    input/input.go              # ReadMessage() — arg/file/stdin
```

## Тестирование

```bash
go test ./...
```

Тесты используют `httptest.Server` для HTTP-вызовов, `t.TempDir()` для файловых операций и `t.Setenv()` для переменных окружения.
