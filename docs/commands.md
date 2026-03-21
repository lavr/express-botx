# Команды

Полный справочник команд express-botx.

## Обзор

| Команда | Описание |
|---------|----------|
| `send` | Отправить сообщение и/или файл в чат |
| `api` | Отправить произвольный HTTP-запрос к BotX API |
| `enqueue` | Положить сообщение в очередь для асинхронной отправки |
| `serve` | Запустить HTTP-сервер (API + вебхуки) |
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

## Общие флаги

Доступны для большинства команд:

```
--host          хост сервера eXpress
--bot-id        ID бота (UUID)
--bot           имя бота из конфига
--secret        секрет бота (литерал, env:VAR или vault:path#key)
--token         токен бота (альтернатива --secret)
--config        путь к файлу конфигурации
--no-cache      отключить кэширование токена
--format        формат вывода: text или json (по умолчанию: text)
-v / -vv / -vvv уровень подробности логирования
```

---

## send

Отправляет сообщение в чат через BotX API.

### Примеры

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

# Все параметры через флаги (без конфига)
express-botx send --host express.company.ru --bot-id UUID --secret KEY --chat-id UUID "Hello"
```

При успехе утилита завершается молча (exit 0). Ошибки выводятся в stderr (exit 1).

### Флаги

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

---

## api

Отправляет произвольный HTTP-запрос к eXpress BotX API с автоматической аутентификацией. Поддерживает JSON-тело через `-f`/`-F`, raw body через `--input`, multipart-загрузку через `--input @file`, фильтрацию ответа через jq-выражения (`-q`).

### Примеры

```bash
# GET-запрос
express-botx api /api/v3/botx/chats/list

# GET с query-параметрами
express-botx api '/api/v3/botx/chats/info?group_chat_id=<UUID>'

# POST с JSON-телом из полей
express-botx api -X POST /api/v3/botx/chats/create -f name=test -f chat_type=group_chat

# POST с JSON-телом из файла (raw mode)
express-botx api -X POST /api/v4/botx/notifications/direct \
  --input payload.json -H 'Content-Type: application/json'

# POST raw body с кастомным Content-Type
express-botx api -X POST /api/v3/botx/smartapps/event \
  --input event.xml -H 'Content-Type: application/xml'

# Загрузить файл (multipart)
express-botx api -X POST /api/v3/botx/files/upload \
  --input @photo.jpg \
  -f group_chat_id=<UUID> -f file_name=photo.jpg -f mime_type=image/jpeg

# Скачать файл
express-botx api '/api/v3/botx/files/download?group_chat_id=<UUID>&file_id=<UUID>' > photo.jpg

# Фильтрация через jq
express-botx api /api/v3/botx/chats/list -q '.result[].name'

# Показать заголовки ответа
express-botx api -i /api/v3/botx/chats/list
```

При HTTP 2xx — exit 0. При non-2xx — тело ответа выводится в stdout, exit 1. Ошибки валидации и auth выводятся в stderr (exit 1, stdout пустой).

### Флаги

```
-X, --method     HTTP-метод (авто: POST при -f/-F/--input, иначе GET)
-f, --field      строковое поле для JSON-тела (key=value, повторяемый)
-F               типизированное поле: true/false → bool, числа → number, @file → содержимое
-H, --header     дополнительный HTTP-заголовок (key:value, повторяемый)
--input          файл с телом запроса (- для stdin, @file для multipart)
--part-name      имя multipart-part для бинарного файла (по умолчанию: content)
-q, --jq         jq-выражение для фильтрации JSON-ответа
-i, --include    показать HTTP-статус и заголовки ответа
--timeout        таймаут запроса (перезаписывает значение из конфига)
--silent         подавить вывод тела ответа
```

### Режимы тела запроса

| Режим | Флаги | Content-Type |
|-------|-------|-------------|
| JSON | `-f`/`-F` | `application/json` (авто) |
| Raw | `--input file` | не выставляется — задать через `-H` |
| Multipart | `--input @file` [+ `-f`] | `multipart/form-data` (авто) |

`-f`/`-F` и `--input` (без `@`) взаимоисключающие. `-F` запрещён в multipart-режиме.

---

## enqueue

Кладёт сообщение в очередь (RabbitMQ / Kafka) вместо прямой отправки в BotX API. Требует сборки с соответствующим build tag.

### Примеры

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

### Флаги

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
|-------|----------|
| `direct` | Producer получает `--bot-id` и `--chat-id` (UUID) и публикует без проверки. Не нужен catalog. |
| `catalog` | Алиасы (`--bot`, `--chat-id` по имени) резолвятся через локальный snapshot каталога. |
| `mixed` | Если указаны UUID — работает как `direct`. Если алиасы — через catalog. Рекомендуемый default. |

---

## serve

Запускает HTTP-сервер с эндпоинтами для отправки сообщений и приёма вебхуков.

### Примеры

```bash
express-botx serve --config config.yaml
express-botx serve --config config.yaml --listen :9090
express-botx serve --config config.yaml --api-key env:MY_API_KEY
```

### Эндпоинты

| Метод | Путь | Описание |
|-------|------|----------|
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

---

## worker

Читает сообщения из очереди, отправляет в BotX API, публикует результаты в reply queue.

### Примеры

```bash
# Запуск worker'а
express-botx worker --config config.yaml

# С health check HTTP-сервером
express-botx worker --config config.yaml --health-listen :8081

# Без публикации каталога
express-botx worker --config config.yaml --no-catalog-publish
```

По умолчанию worker публикует routing catalog в отдельную queue/topic, чтобы producer'ы могли резолвить алиасы.

### Флаги

```
--health-listen       адрес для health check сервера (например, :8081)
--no-catalog-publish  отключить публикацию каталога
```

### Health check

При `--health-listen` worker поднимает HTTP-сервер:

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/healthz` | 200 если consumer подключён к брокеру, 503 иначе |
| `GET` | `/readyz` | 200 когда worker готов принимать сообщения, 503 при startup/shutdown |

---

## bot

### bot ping

Проверяет авторизацию и доступность API:

```bash
express-botx bot ping
express-botx bot ping --bot prod
```

### bot info

Показывает информацию о боте:

```bash
express-botx bot info
express-botx bot info --bot prod --format json
```

### bot token

Получает токен бота для использования в скриптах:

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

---

## chats

### chats list

```bash
express-botx chats list
express-botx chats list --bot prod --format json
```

### chats info

```bash
express-botx chats info --chat-id UUID
express-botx chats info --chat-id alerts
```

---

## user search

Поиск пользователя по email, HUID или AD-логину:

```bash
express-botx user search --email user@company.ru
express-botx user search --huid UUID
express-botx user search --ad-login jdoe
```

---

## config

### config bot add

По умолчанию обменивает secret на token через API и сохраняет **только token** (secure by default):

```bash
# Secret → token (secret не сохраняется)
express-botx config bot add --name prod --host h --bot-id ID --secret SECRET

# Сохранить secret как есть
express-botx config bot add --name prod --host h --bot-id ID --secret SECRET --save-secret

# Готовый token
express-botx config bot add --name prod --host h --bot-id ID --token TOKEN
```

### config bot rm / list

```bash
express-botx config bot list
express-botx config bot rm prod
```

### config chat add

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

При `--name` выполняется поиск по подстроке (case-insensitive). Если найдено несколько чатов — выводится список для уточнения. Если `--alias` не указан — генерируется из имени чата (`"Deploy Alerts"` → `deploy-alerts`, `"Веб-админы"` → `veb-adminy`).

### config chat set

```bash
express-botx config chat set general UUID --default
express-botx config chat set general UUID --no-default
```

### config chat import

Импортирует все чаты бота в конфиг. По умолчанию — только `group_chat`.

```bash
# Базовый импорт
express-botx config chat import

# Dry run
express-botx config chat import --dry-run

# Только конференции
express-botx config chat import --only-type voex_call

# С префиксом и привязкой к боту
express-botx config chat import --bot deploy-bot --prefix team-
```

Флаги:

```
--dry-run        показать что будет импортировано, без изменений
--only-type      group_chat | voex_call
--prefix         префикс для алиасов
--skip-existing  пропускать конфликты алиасов
--overwrite      перезаписывать конфликтующие алиасы
```

Поведение по умолчанию безопасное: при конфликте алиасов — ошибка.

### config chat rm / list

```bash
express-botx config chat list
express-botx config chat rm deploy
```

### config apikey add

```bash
# Сгенерировать случайный ключ
express-botx config apikey add --name monitoring

# Добавить конкретное значение
express-botx config apikey add --name monitoring --key "my-secret-key"

# Ссылка на переменную окружения
express-botx config apikey add --name grafana --key "env:GRAFANA_API_KEY"

# Ссылка на Vault
express-botx config apikey add --name ci --key "vault:secret/data/express#ci_api_key"
```

### config apikey rm / list

```bash
express-botx config apikey list   # значения скрыты
express-botx config apikey rm monitoring
```

### config show

```bash
express-botx config show
```

Показывает путь к конфигу и сводку (боты, чаты, ключи).
