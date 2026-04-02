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
| `bot token get` | Получить токен бота (из кеша или API) |
| `bot token refresh` | Принудительно обновить токен и записать в кеш |
| `bot token clear` | Удалить закешированный токен |
| `chats list` | Показать список чатов бота |
| `chats info` | Показать детальную информацию о чате |
| `user search` | Найти пользователя по email, HUID или AD-логину |
| `config bot add\|rm\|list` | Управление ботами в конфиге |
| `config chat add\|set\|import\|rm\|list` | Управление алиасами чатов |
| `config apikey add\|rm\|list` | Управление API-ключами сервера |
| `config show` | Показать путь к конфигу и сводку |
| `config edit` | Открыть конфиг в редакторе с валидацией |
| `config validate` | Проверить конфиг: синтаксис, поля, форматы, ссылки |

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
--all / -A      итерировать все боты из конфига (bot ping/info/token, chats list, config chat import)
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

# С mentions (BotX wire-формат)
express-botx send --mentions '[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Иван"}}]' \
  "@{mention:aaa-bbb} Привет!"

# С inline mentions (автоматический парсинг)
express-botx send "Привет, @mention[email:user@company.ru]!"
express-botx send "Задача для @mention[email:user@company.ru;Иван%20Петров]"
express-botx send "@mention[all] Внимание!"
express-botx send "Проверь @mention[huid:f16cdc5f-6366-5552-9ecd-c36290ab3d11;Иван]"

# Inline mentions + raw mentions одновременно
express-botx send --mentions '[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Иван"}}]' \
  "@{mention:aaa-bbb} и @mention[email:other@company.ru] — готово"

# Отключить парсинг inline mentions
express-botx send --no-parse "Текст с @mention[email:...] останется как есть"

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
--mentions      JSON-массив mentions в wire-формате BotX API
--no-parse      отключить парсинг inline @mention[...] в тексте
```

Поле `--mentions` принимает JSON-массив в формате BotX API. Текст сообщения должен уже содержать
соответствующие placeholder'ы (`@{mention:...}`, `@@{mention:...}`, `##{mention:...}`).
Если JSON невалиден или не является массивом, команда завершится с ошибкой.

### Inline mentions

По умолчанию parser включён и ищет в тексте сообщения токены `@mention[...]`.
Поддерживаемый синтаксис:

- `@mention[email:<email>]` — mention по email (выполняется lookup пользователя);
- `@mention[email:<email>;<display_name>]` — с явным display name (URL-encoded);
- `@mention[huid:<uuid>]` — mention по HUID (без lookup);
- `@mention[huid:<uuid>;<display_name>]` — с явным display name;
- `@mention[all]` — broadcast mention на весь чат.

Parser заменяет найденные токены на BotX placeholder'ы (`@{mention:<id>}`) и добавляет
соответствующие записи в массив mentions. Если указаны и `--mentions`, и inline токены,
массивы объединяются: raw mentions остаются без изменений, parsed mentions дописываются в конец.

При ошибке парсинга или lookup токен остаётся в тексте как есть, сообщение всё равно отправляется.

Флаг `--no-parse` отключает парсинг: токены `@mention[...]` остаются в тексте без изменений.

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

# С mentions (BotX wire-формат)
express-botx enqueue --mentions '[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Иван"}}]' \
  --bot-id UUID --chat-id UUID "@{mention:aaa-bbb} Привет!"

# С inline mentions
express-botx enqueue --bot-id UUID --chat-id UUID "Привет, @mention[email:user@company.ru]!"

# Отключить парсинг inline mentions
express-botx enqueue --no-parse --bot-id UUID --chat-id UUID "Текст с @mention[email:...] как есть"
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
--mentions       JSON-массив mentions в wire-формате BotX API
--no-parse       отключить парсинг inline @mention[...] в тексте
```

Поле `--mentions` принимает JSON-массив в формате BotX API. Текст сообщения должен уже содержать
соответствующие placeholder'ы (`@{mention:...}`, `@@{mention:...}`, `##{mention:...}`).
Если JSON невалиден или не является массивом, команда завершится с ошибкой.

Inline mentions (`@mention[...]`) поддерживаются аналогично команде `send`. Парсинг включён
по умолчанию, отключается через `--no-parse`. В очередь публикуются уже нормализованные
`message` и merged `mentions`.

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

### Поля HTTP payload

Эндпоинт `/send` принимает `application/json` и `multipart/form-data`. Основные поля:

| Поле | Тип | Описание |
|------|-----|----------|
| `chat_id` | string | UUID или алиас чата (обязательно) |
| `message` | string | Текст сообщения |
| `file` | object | Вложение: `{"name": "...", "data": "base64..."}` |
| `status` | string | `ok` или `error` (по умолчанию: `ok`) |
| `metadata` | JSON | Произвольный JSON для `notification.metadata` |
| `mentions` | JSON array | Массив mentions в wire-формате BotX API |
| `opts` | object | Опции доставки: `silent`, `stealth`, `force_dnd`, `no_notify` |

Пример с mentions через HTTP API:

```bash
curl -X POST http://localhost:8080/api/v1/send \
    -H "Authorization: Bearer <api-key>" \
    -H "Content-Type: application/json" \
    -d '{
      "chat_id": "CHAT-UUID",
      "message": "@{mention:aaa-bbb} Deploy completed",
      "mentions": [{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Иван"}}]
    }'
```

Поле `mentions` принимает JSON-массив в формате BotX API. Текст сообщения должен уже содержать
соответствующие placeholder'ы (`@{mention:...}`, `@@{mention:...}`, `##{mention:...}`).
При multipart-запросе `mentions` передаётся как строковое JSON-поле формы.

### Inline mentions в HTTP API

По умолчанию parser включён для HTTP-запросов. Токены `@mention[...]` в поле `message`
автоматически парсятся и нормализуются в BotX wire-format. Синтаксис аналогичен CLI (см. `send`).

Пример с inline mention:

```bash
curl -X POST http://localhost:8080/api/v1/send \
    -H "Authorization: Bearer <api-key>" \
    -H "Content-Type: application/json" \
    -d '{
      "chat_id": "CHAT-UUID",
      "message": "Привет, @mention[email:user@company.ru]!"
    }'
```

Отключение парсинга через query parameter `?no_parse=true`:

```bash
curl -X POST 'http://localhost:8080/api/v1/send?no_parse=true' \
    -H "Authorization: Bearer <api-key>" \
    -H "Content-Type: application/json" \
    -d '{
      "chat_id": "CHAT-UUID",
      "message": "Текст с @mention[email:...] останется как есть"
    }'
```

При ошибке парсинга или lookup сообщение всё равно отправляется (HTTP 200), токен остаётся в тексте.

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

# Проверить все боты из конфига
express-botx bot ping --all
express-botx bot ping -A --format json
```

При `--all` / `-A` итерирует все боты из конфига и выводит статус каждого. Текстовый формат: `botname: OK 123ms` или `botname: FAIL reason`. JSON: массив объектов с полями `name`, `status`, `elapsed_ms`, `error`. Exit code ненулевой, если хотя бы один бот недоступен. Флаг `--all` несовместим с `--bot`, `--host`, `--bot-id`, `--secret`, `--token`.

### bot info

Показывает информацию о боте:

```bash
express-botx bot info
express-botx bot info --bot prod --format json

# Информация по всем ботам
express-botx bot info --all
express-botx bot info -A --format json
```

При `--all` / `-A` выводит таблицу (text) или массив (json) с информацией по каждому боту из конфига. Флаг `--all` несовместим с `--bot`, `--host`, `--bot-id`, `--secret`, `--token`.

### bot token get

Получает токен бота. По умолчанию возвращает закешированный токен, если он есть; иначе запрашивает новый через API и кеширует.

```bash
# Из кеша или API
express-botx bot token get --bot prod

# Всегда запросить свежий (bypass кеша), но обновить кеш
express-botx bot token get --bot prod --fresh

# Использование в скриптах
TOKEN=$(express-botx bot token get --bot prod)

# Статический токен — возвращает как есть
express-botx bot token get --bot token-bot

# Токены всех ботов
express-botx bot token get --all
express-botx bot token get -A --format json
```

Флаг `--fresh` заставляет обойти кеш и запросить токен из API, при этом новый токен сохраняется в кеш. При `--all` / `-A` выводит токены всех ботов. Флаг `--all` несовместим с `--bot`, `--host`, `--bot-id`, `--secret`, `--token`.

### bot token refresh

Принудительно обновляет токен: всегда запрашивает новый через API и записывает в кеш. Не работает для ботов со статическим токеном.

```bash
express-botx bot token refresh --bot prod
express-botx bot token refresh --all
express-botx bot token refresh -A --format json
```

### bot token clear

Удаляет закешированный токен. Для ботов со статическим токеном или при отключённом кеше — no-op.

```bash
express-botx bot token clear --bot prod
express-botx bot token clear --all
```

---

## chats

### chats list

```bash
express-botx chats list
express-botx chats list --bot prod --format json

# Чаты всех ботов
express-botx chats list --all
express-botx chats list -A --format json
```

При `--all` / `-A` собирает чаты со всех ботов из конфига. В текстовом формате группирует по боту, в JSON добавляет поле `bot_name` к каждой записи. Чаты от успешных ботов выводятся даже если другие боты упали. Флаг `--all` несовместим с `--bot`, `--host`, `--bot-id`, `--secret`, `--token`.

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

# Импорт чатов от всех ботов
express-botx config chat import --all
express-botx config chat import -A --dry-run --format json
```

При `--all` / `-A` импортирует чаты от каждого бота из конфига. Алиасы включают имя бота для предотвращения коллизий (например, `botname-chatname`). Импортированные чаты привязываются к боту-источнику через поле `bot`. Флаги `--dry-run`, `--only-type`, `--prefix`, `--skip-existing`, `--overwrite` применяются ко всем ботам. Флаг `--all` несовместим с `--bot`, `--host`, `--bot-id`, `--secret`, `--token`.

Флаги:

```
--all, -A        импортировать чаты от всех ботов из конфига
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

### config edit

Открывает файл конфигурации в текстовом редакторе. После сохранения валидирует YAML и применяет изменения. Работает по аналогии с `kubectl edit`.

```bash
# Открыть конфиг в $EDITOR (или vi по умолчанию)
express-botx config edit

# Указать конкретный файл конфига
express-botx config edit --config /path/to/config.yaml
```

Поведение:
- Редактор определяется из переменной окружения `$EDITOR`, при отсутствии — `vi`
- Если файл не изменён — выводит "Edit cancelled, no changes made"
- После сохранения валидирует YAML-структуру и настройки ботов/чатов
- При ошибке валидации предлагает: `[r]etry editing / [d]iscard changes? (r/d)`
  - `r` — вернуться в редактор для исправления
  - `d` — отменить все изменения
- При успешной валидации записывает изменения и выводит "Config updated: <path>"

Флаги:

```
--config    путь к файлу конфигурации
```

### config validate

Проверяет файл конфигурации без подключения к серверам: YAML-синтаксис, известные поля, обязательные поля, форматы значений, перекрёстные ссылки.

```bash
# Проверить конфиг
express-botx config validate

# Указать конкретный файл
express-botx config validate --config /path/to/config.yaml

# Вывод в JSON
express-botx config validate --format json
```

Поведение:
- Выводит список проблем: `[ERROR] path: message` или `[WARN] path: message`
- В конце — строка итогов: "N errors, M warnings"
- При наличии ошибок — exit 1, при наличии только предупреждений — exit 0
- Не резолвит секреты и не проверяет доступность серверов

Проверки:
- Неизвестные ключи в YAML (предупреждения)
- Обязательные поля: `host` и `id` для ботов, `secret` или `token` (но не оба)
- Форматы: UUID для `id` и `chat_id`, длительности (`timeout`, `retry_backoff`), допустимые enum-значения (`cache.type`, `queue.driver`, `routing_mode`)
- Перекрёстные ссылки: `bot` в чате ссылается на существующего бота, не более одного чата по умолчанию, `default_chat_id` в alertmanager/grafana ссылается на существующий алиас

Флаги:

```
--config    путь к файлу конфигурации
--format    формат вывода: text или json (по умолчанию: text)
```
