# RFC-019: Очередь для асинхронной отправки (`enqueue` + `worker` + embedded queue-catalog)

- **Статус:** Draft
- **Дата:** 2026-03-18

## Контекст

Сейчас `express-botx` отправляет сообщения синхронно:

- `express-botx send` сразу вызывает BotX API
- `express-botx serve` принимает HTTP-запрос и в рамках этого же запроса вызывает BotX API

Этого достаточно для простого CLI и webhook-сценариев, но не хватает для production-схем, где нужно:

- быстро принять запрос и не держать клиент до ответа upstream
- отделить ingress API от фактической отправки
- масштабировать API и worker независимо
- переживать временную недоступность BotX API через брокер
- не распространять бот-секреты на producer-сторону

Дополнительные ограничения:

- producer не должен знать `secret`/`token`
- не хочется раздавать routing-конфиг через HTTP
- между producer и worker может не быть прямой сети, кроме брокера
- нужен optimistic-режим: если producer получил конкретные `bot_id` и `chat_id`, он просто публикует сообщение в очередь без предварительной валидации

## Решение

Принять **Вариант A + B**, но с queue-based catalog вместо HTTP-discovery:

- один бинарник `express-botx`
- новые субкоманды:
  - `enqueue`
  - `worker`
- режим `serve --enqueue` для HTTP API
- драйверы очереди подключаются через build tags:
  - `rabbitmq`
  - `kafka`

Итоговый UX:

```bash
express-botx send "hello"                        # синхронно, как сейчас
express-botx enqueue "hello"                     # положить в очередь
express-botx serve                               # HTTP -> BotX синхронно
express-botx serve --enqueue                     # HTTP -> очередь
express-botx worker                              # очередь -> BotX
```

### Почему не отдельные бинарники

На текущем этапе отдельные бинарники не дают достаточной выгоды:

- проект уже живёт на одном top-level dispatcher
- проще оставить один Docker image и один Helm chart
- разные роли можно запускать разными `command/args` в отдельных Deployment

Build tags решают основной минус одного бинарника: не нужно тянуть зависимости RabbitMQ/Kafka во все сборки.

## Режимы маршрутизации

### `direct`

Producer получает конкретные `bot_id` и `chat_id` и публикует сообщение в очередь без проверки существования чата/бота.

Это режим "оптимистичного" продюсера:

- producer не знает chats aliases
- producer не знает bot secrets
- broker acceptance означает только "сообщение принято в очередь", а не "сообщение валидно и будет доставлено"

### `catalog`

Producer держит локальный read-only cache публичного routing-catalog, полученного через отдельную catalog queue/topic, и резолвит alias локально до публикации в work queue.

### `mixed`

Если в запросе есть конкретные target IDs, producer работает как `direct`. Если пришли alias/логические имена, producer использует локальный catalog cache.

Это рекомендуемый default для `enqueue` и `serve --enqueue`.

## CLI-контракт

### `express-botx enqueue`

Кладёт сообщение в очередь вместо прямой отправки в BotX.

Поведение по полезной нагрузке должно совпадать с `send`:

- те же источники текста: positional arg, `--body-from`, `stdin`
- те же флаги вложений и notification options

Новый routing-контракт:

```text
--routing-mode      direct | catalog | mixed    (default: mixed)
--bot-id            bot UUID для direct routing
--bot               bot alias для catalog routing
--chat-id           chat UUID или alias, в зависимости от routing mode
```

Флаги `--bot-id`, `--bot`, `--chat-id` переиспользуются из `send`. Auth-флаги (`--secret`, `--token`) для `enqueue` нерелевантны — producer не аутентифицируется в BotX API.

На выходе команда печатает `request_id`:

- `text`: только UUID
- `json`: объект с `ok`, `queued`, `request_id`

### `express-botx serve --enqueue`

Переводит HTTP `/send` в асинхронный режим:

- вместо прямой отправки публикует задание в очередь
- возвращает `202 Accepted`
- тело ответа:

```json
{
  "ok": true,
  "queued": true,
  "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2"
}
```

Синхронный режим `serve` остаётся без изменений и продолжает возвращать `sync_id`.

HTTP payload для async-mode расширяется direct-routing полями:

```json
{
  "routing_mode": "direct",
  "bot_id": "bot-uuid",
  "chat_id": "chat-uuid",
  "message": "deploy ok"
}
```

### `express-botx worker`

Читает сообщения из work queue, отправляет их в BotX API и при необходимости публикует результат в reply queue/topic.

Дополнительно `worker` по умолчанию публикует public routing catalog в отдельную catalog queue/topic:

- на старте
- периодически
- при возможности, при reload/изменении конфига

Это поведение включено по умолчанию и отключается явным флагом:

```text
--no-catalog-publish
```

Если deployment работает в pure `direct` mode, catalog publishing можно просто выключить.

## Конфиг по ролям

### Producer

Минимальный конфиг producer'а:

```yaml
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
  reply_queue: express-botx-replies

producer:
  routing_mode: mixed

catalog:
  queue_name: express-botx-catalog
  cache_file: /var/lib/express-botx/catalog.json
  max_age: 10m
```

Producer'у не нужны:

- `secret`
- `token`
- полный список ботов в приватном конфиге

### Worker

Минимальный конфиг worker'а для optimistic/direct mode:

```yaml
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
  group: express-botx

worker:
  concurrency: 5

catalog:
  queue_name: express-botx-catalog
  publish_interval: 30s
  publish: true

bots:
  alerts:
    host: express.company.ru
    id: bot-uuid
    secret: env:ALERTS_SECRET
```

Если нужны alias/default и catalog publishing, добавляется `chats:`:

```yaml
chats:
  deploy:
    id: chat-uuid
    bot: alerts
  general:
    id: chat-uuid-2
    default: true
```

Правила:

- producer и worker используют тот же брокер, что и work queue
- отдельный HTTP transport для каталога не нужен
- отдельная авторизация между producer и worker не нужна сверх авторизации в брокере
- если выбран драйвер, не собранный в бинарник, команда возвращает понятную ошибку:

```text
queue driver "kafka" is not compiled in; rebuild with -tags kafka
```

## Сериализация

Все сообщения в очередях (work, result, catalog) сериализуются в **JSON**, без compression в v1.

Assumption по масштабу catalog: десятки-сотни записей (ботов/чатов), не тысячи. Полный snapshot при таком масштабе — единицы килобайт.

## Файлы в async-режиме

В async-режиме (`enqueue`, `serve --enqueue`) файл сериализуется в base64 внутри JSON work message. Это увеличивает размер ~на 33%.

Ограничение размера файла настраивается:

```yaml
queue:
  max_file_size: 1MB   # default: 1 MiB
```

CLI: `--queue-max-file-size 1MB`

При превышении лимита:
- `enqueue`: ошибка с указанием лимита и рекомендацией использовать `send`
- `serve --enqueue`: HTTP 413 с тем же сообщением

Синхронный режим (`send`, `serve` без `--enqueue`) сохраняет текущий лимит 32 MiB и не затрагивается этой настройкой.

`serve --enqueue` принимает и `application/json`, и `multipart/form-data` — так же как текущий синхронный `/send`.

Новые form fields для `multipart/form-data` в async-режиме:

- `routing_mode` — `direct` | `catalog` | `mixed` (опционально, default из конфига)
- `bot_id` — UUID бота (для direct routing)

Существующие поля без изменений: `bot`, `chat_id`, `message`, `status`, `opts`, `metadata`, `file`.

JSON payload аналогичен — добавляются `routing_mode` и `bot_id`.

## Контракт work-сообщения

В рабочую очередь кладётся concrete routing, а не "сырая" команда.

Это сохраняет детерминизм: queued message не "поплывёт" после изменения alias или bot-binding.

```json
{
  "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2",
  "routing": {
    "host": "express.company.ru",
    "bot_id": "bot-uuid",
    "chat_id": "chat-uuid",
    "bot_name": "alerts",
    "chat_alias": "deploy",
    "catalog_revision": "2026-03-18T10:15:00Z:17"
  },
  "payload": {
    "message": "deploy ok",
    "status": "ok",
    "file": null,
    "opts": {
      "silent": false,
      "stealth": false,
      "force_dnd": false,
      "no_notify": false
    },
    "metadata": null
  },
  "reply_to": "express-botx-replies",
  "enqueued_at": "2026-03-18T10:15:05Z"  // ставит producer
}
```

Поля `host`, `bot_name`, `chat_alias`, `catalog_revision` опциональны и нужны для трассировки. Для фактической отправки worker использует только:

- `bot_id` — уникален глобально, worker находит bot credentials и host в своём конфиге
- `chat_id`

## Контракт result-сообщения

```json
{
  "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2",
  "status": "sent",
  "sync_id": "5f1d58d3-4d2a-4f97-a0a8-2e5f9f7a7dcb",
  "error": "",
  "sent_at": "2026-03-18T10:15:06Z"
}
```

Для ошибки:

```json
{
  "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2",
  "status": "failed",
  "error": "unknown bot_id bot-uuid",
  "sent_at": "2026-03-18T10:15:06Z"
}
```

## Контракт catalog snapshot

Catalog распространяется через отдельную queue/topic полными snapshots, а не дельтами.

```json
{
  "type": "catalog.snapshot",
  "revision": "2026-03-18T10:15:00Z:17",
  "generated_at": "2026-03-18T10:15:00Z",
  "bots": [
    {"name": "alerts", "host": "express.company.ru", "id": "bot-uuid"}
  ],
  "chats": [
    {"name": "deploy", "id": "chat-uuid", "bot": "alerts"},
    {"name": "general", "id": "chat-uuid-2", "default": true}
  ]
}
```

Принципы:

- snapshot всегда полный
- producer хранит локальный cache последнего snapshot
- producer в `catalog`/`mixed` mode работает из локального cache, а не делает синхронный запрос в runtime
- при отсутствии свежего cache alias-routing недоступен, но direct-routing продолжает работать

## Семантика доставки

Базовая семантика первой версии:

- доставка в BotX: **at-least-once**
- reply queue: **best-effort**
- catalog queue: **eventually consistent**
- exactly-once не обещаем

Следствия:

- `request_id` обязателен для трассировки и дедупликации на стороне потребителя результата
- успешная публикация в broker означает только "сообщение принято в очередь"
- в optimistic/direct mode worker может позже вернуть `failed`, если bot не найден или BotX отверг запрос
- если publish reply-сообщения не удался после успешной отправки в BotX, worker логирует ошибку, но исходное сообщение считается обработанным

## Retry policy

Worker при ошибке отправки в BotX API применяет retry с exponential backoff:

- **Попытки:** 3 (настраивается через `worker.retry_count`)
- **Backoff:** 1s, 5s, 25s (base × 5^attempt, настраивается через `worker.retry_backoff`)
- **После исчерпания попыток:**
  - RabbitMQ: nack без requeue — сообщение уходит в DLQ, если настроен на стороне брокера
  - Kafka: commit offset + log error на уровне ERROR
- **В обоих случаях:** публикуется result с `status: "failed"` в reply queue (если настроена)

Конфиг:

```yaml
worker:
  concurrency: 5
  retry_count: 3
  retry_backoff: 1s
```

Не ретраим:

- ошибки валидации (unknown bot_id, неверный формат сообщения) — сразу `failed`
- 401 после успешного token refresh — считается persistent failure

## Graceful shutdown

Worker обрабатывает SIGTERM/SIGINT:

1. Прекращает consume новых сообщений из брокера
2. Ждёт завершения in-flight сообщений (до `--shutdown-timeout`, default 30s)
3. По истечении таймаута — force exit; незавершённые сообщения не ack'аются, брокер выполнит redelivery

Конфиг:

```yaml
worker:
  shutdown_timeout: 30s
```

Для Kubernetes рекомендуется `terminationGracePeriodSeconds` ≥ `shutdown_timeout` + запас на preStop hook.

## Health check для worker

Worker поднимает минимальный HTTP-сервер для health check и (в будущем) метрик:

```
--health-listen :8081
```

Эндпоинты:

- `GET /healthz` — 200 если consumer подключён к брокеру и обрабатывает сообщения; 503 иначе
- `GET /readyz` — 200 когда worker готов принимать сообщения; 503 во время startup/shutdown

Конфиг:

```yaml
worker:
  health_listen: ":8081"
```

В будущем на этот же порт добавится `GET /metrics` для Prometheus. Архитектура health-сервера должна учитывать это расширение.

## Observability

### v1: минимальный набор

**APM:** каждый обработанный work message оборачивается в APM transaction (через существующий `internal/apm`).

**Structured logging** на каждое обработанное сообщение:

- `request_id`
- `bot_id`
- `chat_id`
- `duration`
- `status` (sent / failed)
- `error` (при failure)
- `attempt` (номер попытки при retry)

Уровни:
- успешная отправка: INFO
- retry: WARN
- исчерпание попыток: ERROR

### Будущее (не в scope v1)

- Prometheus метрики: messages processed/sec, error rate, processing latency histogram, consumer lag
- Эндпоинт `GET /metrics` на health-listen порту

## Особенности Kafka и RabbitMQ

### Kafka

**Work queue:** обычный topic, consumer group для worker'ов.

**Catalog:** отдельный compacted topic, **single partition**. Producer на старте получает end offset, выполняет seek на `end - 1` и читает одну запись — последний snapshot. Далее слушает новые snapshots в реальном времени. Не зависит от `auto.offset.reset` и consumer group. Если topic пуст (`end offset == 0`) — snapshot отсутствует, producer работает в direct mode, alias-routing недоступен до первого snapshot.

### RabbitMQ

**Work queue:** обычная queue с competing consumers (worker'ы).

**Catalog:** worker публикует в **fanout exchange** (`express-botx-catalog`). Каждый producer-инстанс создаёт **exclusive auto-delete queue**, привязанную к этому exchange. Каждый producer получает копию каждого snapshot. При отключении producer его queue удаляется автоматически.

Следствия для RabbitMQ:
- worker должен публиковать полный snapshot периодически (producer мог подключиться после предыдущего snapshot)
- producer обязан хранить local cache на диске (на случай рестарта до получения первого snapshot)
- на старте producer может работать в direct mode даже без свежего каталога

В первой версии не опираемся на broker-specific plugins вроде last-value cache.

## Интерфейс очереди

```go
type WorkMessage struct {
    RequestID  string
    Routing    Routing
    Payload    Payload
    ReplyTo    string
    EnqueuedAt time.Time
}

type Routing struct {
    Host            string
    BotID           string
    ChatID          string
    BotName         string
    ChatAlias       string
    CatalogRevision string
}

type WorkResult struct {
    RequestID string
    Status    string
    SyncID    string
    Error     string
    SentAt    time.Time
}

type CatalogSnapshot struct {
    Revision    string
    GeneratedAt time.Time
    Bots        []config.BotEntry
    Chats       []config.ChatEntry
}

type Publisher interface {
    PublishWork(ctx context.Context, msg *WorkMessage) error
    PublishResult(ctx context.Context, topic string, res *WorkResult) error
    PublishCatalog(ctx context.Context, topic string, snapshot *CatalogSnapshot) error
    Close() error
}

type Consumer interface {
    ConsumeWork(ctx context.Context, handler func(context.Context, *WorkMessage) error) error
    ConsumeCatalog(ctx context.Context, handler func(context.Context, *CatalogSnapshot) error) error
    Close() error
}
```

Рекомендуемая структура:

- `internal/queue/work.go`
- `internal/queue/catalog.go`
- `internal/queue/factory.go`
- `internal/queue/rabbitmq/...`
- `internal/queue/kafka/...`

Build tags:

- rabbitmq implementation: `//go:build rabbitmq`
- kafka implementation: `//go:build kafka`
- fallback stubs без тега возвращают ошибку `driver not compiled in`

## Шаги реализации

### Шаг 1. Зафиксировать runtime-контракт: роли, config, flags, routing modes

Изменения:

- [x] расширить `internal/config.Config` секциями `Queue`, `Producer`, `Worker`, `Catalog`
- [x] добавить parsing новых флагов:
  - `--routing-mode`
  - `--no-catalog-publish`
  - queue/catalog flags
- [x] зафиксировать семантику глобальных флагов для `enqueue`:
  - `--bot` — алиас бота, резолвится через catalog (catalog/mixed mode)
  - `--bot-id` — UUID бота, direct routing (direct/mixed mode)
  - `--chat-id` — UUID или алиас чата
  - `--secret`, `--token` — игнорируются (producer не делает auth)
- [x] обновить usage/help и top-level dispatcher в `internal/cmd/cmd.go`
- [x] определить response-модели для async-режима (`request_id`, `queued`)
- [x] зафиксировать, что `direct` mode не требует `chats:` ни у producer, ни у worker
- [x] зафиксировать, что `worker` публикует catalog по умолчанию, если настроен `catalog.queue_name`
- [x] добавить валидацию уникальности `bot_id`: один и тот же `bot_id` допускается с несколькими алиасами, но только если весь runtime config совпадает (host, secret, token, timeout); при расхождении любого поля — ошибка загрузки конфига. Все алиасы сохраняются и остаются валидными именами — `chats.*.bot` может ссылаться на любой из них. Внутри алиасы с одинаковым `bot_id` резолвятся в один и тот же bot, lookup по `bot_id` всегда однозначен

Проверка:

- [x] unit-тесты на `config.Load` и `config.LoadForServe` для role-specific YAML
- [x] unit-тесты на валидацию `direct/catalog/mixed`
- [x] unit-тесты на валидацию дублей `bot_id`:
   - два алиаса с одинаковым `bot_id` и идентичным runtime config — ок
   - два алиаса с одинаковым `bot_id` и разным secret — ошибка
   - два алиаса с одинаковым `bot_id` и разным host — ошибка
   - `chats.*.bot` ссылается на любой из алиасов — ок
- [x] unit-тесты на обратный индекс `bot_id` → runtime bot:
   - lookup по `bot_id` возвращает однозначный результат при дублях-алиасах
   - lookup по несуществующему `bot_id` — понятная ошибка
- [x] `go test ./internal/config ./internal/cmd`
- [x] help-output показывает `enqueue`, `worker` и новые routing/catalog flags

### Шаг 2. Добавить queue abstraction и build-tag factory

Изменения:

- [x] ввести `internal/queue` с контрактами `WorkMessage`, `WorkResult`, `CatalogSnapshot`
- [x] реализовать factory по `driver`
- [x] добавить build-tag stubs
- [x] подготовить fake-реализации очереди для unit/integration тестов

Проверка:

- [x] `go build -o express-botx .`
- [x] `go build -tags rabbitmq -o express-botx .`
- [x] `go build -tags kafka -o express-botx .`
- [x] `go build -tags "rabbitmq kafka" -o express-botx .`
- [x] unit-тест: при запросе отсутствующего драйвера возвращается понятная ошибка

### Шаг 3. Реализовать optimistic/direct path end-to-end

Изменения:

- [x] новый файл `internal/cmd/enqueue.go`
- [x] новый файл `internal/cmd/worker.go`
- [x] в `enqueue` поддержать `direct` mode:
  - если пришли `bot_id` и `chat_id`, публиковать work message без проверки существования чата
- [x] в `serve --enqueue` поддержать direct payload
- [x] в worker искать bot credentials по `bot_id` (уникален глобально, host берётся из конфига worker'а)
- [x] `chats:` на worker не требуются для отправки direct-сообщений
- [x] worker: graceful shutdown (SIGTERM/SIGINT → drain in-flight → exit)
- [x] worker: health check HTTP-сервер (`--health-listen`, `/healthz`, `/readyz`)
- [x] worker: APM transaction на каждое обработанное сообщение
- [x] worker: structured logging (`request_id`, `bot_id`, `chat_id`, `duration`, `status`, `attempt`)
- [x] worker: retry policy (exponential backoff, nack/commit после исчерпания попыток)

Принцип: direct-mode должен работать даже в deployment, где нет `chats:` и нет catalog consumer.

Проверка:

- [x] unit-тесты `enqueue` и `serve --enqueue` на direct publish
- [x] unit-тест worker на успешный send по `bot_id`
- [x] кейс `unknown bot_id` -> `failed`
- [x] кейс upstream error -> retry -> `failed` после исчерпания попыток
- [x] кейс 401 -> token refresh + retry
- [x] graceful shutdown: in-flight сообщения завершаются, новые не принимаются
- [x] health check: `/healthz` возвращает 200 при живом consumer, 503 при отключении
- [x] `go test ./internal/cmd ./internal/botapi ./internal/server`

### Шаг 4. Реализовать embedded catalog publish в worker и local catalog cache

Изменения:

- [x] встроить в `worker` публикацию полных snapshots в catalog queue/topic
- [x] публикация включена по умолчанию и отключается через `--no-catalog-publish`
- [x] producer получает local catalog cache:
  - загрузка с диска на старте
  - обновление из catalog queue
  - проверка `max_age`
- [x] snapshot строится только из публичной части конфига:
  - `bots`: `name`, `host`, `id`
  - `chats`: `name`, `id`, `bot`, `default`

Проверка:

- [x] unit-тест snapshot builder: секреты не попадают в snapshot
- [x] unit-тест catalog cache: snapshot сохраняется и перечитывается
- [x] unit-тест worker: snapshot публикуется на старте и respect'ит `--no-catalog-publish`
- [x] unit-тест producer: при отсутствии cache direct mode работает, alias mode падает понятной ошибкой
- [x] `go test ./internal/cmd ./internal/config ./internal/queue`

### Шаг 5. Реализовать `catalog` и `mixed` routing на producer-стороне

Изменения:

- [x] `enqueue` в `catalog` mode резолвит alias через local snapshot
- [x] `enqueue` в `mixed` mode:
  - использует direct path, если пришли target IDs
  - иначе использует local snapshot
- [x] `serve --enqueue` получает ту же логику
- [x] в work message дописываются `bot_name`, `chat_alias`, `catalog_revision` для observability

Важно: worker не резолвит queued aliases. Alias разрешается только до публикации в work queue.

Проверка:

- [x] unit-тест alias -> concrete route через local snapshot
- [x] кейс stale catalog -> понятная ошибка для alias mode
- [x] кейс direct fields + stale catalog -> сообщение всё равно публикуется
- [x] интеграционный тест `mixed` mode через fake catalog consumer

### Шаг 6. Реализовать драйверы RabbitMQ и Kafka

Изменения:

- [x] `internal/queue/rabbitmq`
- [x] `internal/queue/kafka`
- [x] минимально необходимый контракт:
  - publish work
  - consume work
  - publish result
  - publish/consume catalog snapshots
- [x] настройки ack/commit должны обеспечивать at-least-once semantics для work queue

Рекомендации:

- RabbitMQ: ack после успешного work handler
- Kafka: commit offset после успешного work handler
- Kafka catalog topic делать compacted
- для RabbitMQ worker публикует полный snapshot периодически

Проверка:

- [x] integration tests за build tag `//go:build integration` + соответствующий driver tag; в CI брокеры поднимаются как services
- [x] catalog delivery per-driver:
   - Kafka: producer на старте читает последний snapshot из compacted topic (seek to `end - 1`); после рестарта producer видит актуальный catalog без ожидания нового publish
   - Kafka: пустой topic → producer стартует в direct mode, получает snapshot после первого publish worker'а
   - RabbitMQ: два producer-инстанса одновременно — оба получают каждый snapshot (fanout)
   - RabbitMQ: producer подключается после publish → получает snapshot только после следующего периодического publish worker'а; до этого работает из disk cache или direct mode
- [x] ручной smoke:
   - `worker` публикует snapshot
   - producer поднимает local cache
   - `enqueue` публикует work message
   - `worker` его обрабатывает
   - при `reply_to` публикуется result
- [x] сборки с каждым тегом проходят отдельно

### Шаг 7. Документация, Docker, Helm, эксплуатация

Изменения:

- [x] README: новые команды, routing modes и примеры запуска
- [x] Dockerfile: документировать сборку с `-tags rabbitmq`, `-tags kafka`
- [x] Helm chart:
  - values для `queue.*`, `producer.*`, `worker.*`, `catalog.*`
  - отдельные Deployment patterns:
    - `serve --enqueue`
    - `worker`
  - возможность запускать:
    - pure direct mode
    - mixed mode с worker-side catalog publishing

Проверка:

- [x] README-примеры соответствуют реальному CLI
- [x] Helm template рендерит queue/catalog settings без ручной правки chart
- [x] smoke в k8s/dev:
   - deployment A: `serve --enqueue`
   - deployment B: `worker`
   - direct mode работает без `chats:`

## Что не включаем в первую версию

- HTTP-распространение каталога как зависимость async-фичи
- worker-side alias resolution для queued сообщений
- `send --queue` как алиас `enqueue`
- режим `serve --worker` или комбинированный API+worker в одном процессе
- async-режим для Alertmanager/Grafana webhook handler'ов
- built-in DLQ/retry policy abstraction
- exactly-once semantics
- delta-updates для каталога
- broker-specific plugins для retained/last-value catalog

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | Контракт ролей, config и routing modes | Средняя |
| P0 | `enqueue` direct mode | Средняя |
| P0 | `serve --enqueue` direct mode | Средняя |
| P0 | `worker` direct mode | Средняя |
| P0 | build-tag factory | Низкая |
| P1 | worker-side catalog publish | Средняя |
| P1 | producer local catalog cache | Средняя |
| P1 | `mixed` routing | Средняя |
| P1 | RabbitMQ driver | Средняя |
| P1 | Kafka driver | Средняя |
| P1 | reply queue | Низкая |
| P1 | Helm/README | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/cmd/cmd.go` |
| CREATE | `internal/cmd/enqueue.go` |
| CREATE | `internal/cmd/worker.go` |
| MODIFY | `internal/cmd/serve.go` |
| MODIFY | `internal/server/handler_send.go` |
| MODIFY | `internal/server/server_test.go` |
| MODIFY | `internal/cmd/serve_integration_test.go` |
| MODIFY | `internal/config/config.go` |
| CREATE | `internal/queue/work.go` |
| CREATE | `internal/queue/catalog.go` |
| CREATE | `internal/queue/factory.go` |
| CREATE | `internal/queue/rabbitmq/*` |
| CREATE | `internal/queue/kafka/*` |
| MODIFY | `README.md` |
| MODIFY | `Dockerfile` |
| MODIFY | `charts/express-botx/values.yaml` |
| MODIFY | `charts/express-botx/templates/deployment.yaml` |

## Критерии готовности

Фича считается завершённой, когда выполняются все условия:

1. `express-botx enqueue` в direct mode публикует сообщение в broker без знания chats catalog
2. `express-botx serve --enqueue` принимает direct payload и отвечает `202 Accepted`
3. `express-botx worker` читает сообщение из broker и отправляет его в BotX API по `bot_id` + `chat_id`
4. direct mode работает в deployment без `chats:`
5. `express-botx worker` по умолчанию публикует public routing snapshots в отдельную queue/topic
6. producer в `mixed` mode умеет резолвить alias из local catalog cache
7. при наличии `reply_queue` worker публикует результат с `request_id` и `sync_id/error`
8. бинарник собирается:
   - без queue tags
   - только с `rabbitmq`
   - только с `kafka`
   - с обоими драйверами
9. Helm/chart позволяет разнести API и worker по отдельным Deployment; catalog publishing управляется настройками worker
