# Асинхронные очереди

express-botx поддерживает RabbitMQ и Kafka для надёжной асинхронной доставки сообщений.

## Архитектура

```
┌──────────┐    ┌───────────────┐    ┌─────────┐    ┌──────────┐
│ HTTP API │───▶│ Message Queue │───▶│ Worker  │───▶│ BotX API │
│ (serve   │    │ (RabbitMQ /   │    │         │    │          │
│ --enqueue)│    │  Kafka)       │    │         │    │          │
└──────────┘    └───────────────┘    └─────────┘    └──────────┘
     ▲                                    │
     │          ┌───────────────┐         │
     │          │ Reply Queue   │◀────────┘
     │          └───────────────┘
     │
     ├── CLI: express-botx enqueue
     └── HTTP: POST /api/v1/...
```

**Producer** (HTTP-сервер в режиме `serve --enqueue` или CLI `enqueue`) кладёт сообщение в очередь.
**Worker** забирает из очереди, отправляет в BotX API, публикует результат в reply queue.

## Сборка с поддержкой очередей

Очереди подключаются через build tags. Официальный образ `lavr/express-botx` уже включает драйверы RabbitMQ и Kafka, поэтому эти команды нужны только для кастомной сборки:

```bash
# Из исходников
go build -tags rabbitmq -o express-botx .
go build -tags kafka -o express-botx .
go build -tags "rabbitmq kafka" -o express-botx .

# Docker
docker build --build-arg BUILD_TAGS="sentry rabbitmq" -t express-botx:rabbitmq .
docker build --build-arg BUILD_TAGS="sentry kafka" -t express-botx:kafka .
```

## Конфигурация

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

Producer не нужны `secret`, `token` и полный список ботов — он не аутентифицируется в BotX API.

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

## Режимы маршрутизации (routing modes)

| Режим | Описание | Когда использовать |
|-------|----------|-------------------|
| `direct` | Producer указывает `bot_id` и `chat_id` (UUID). Не нужен catalog. | Простая setup: producer знает UUID. |
| `catalog` | Алиасы резолвятся через routing catalog, который публикует worker. | Producer не знает UUID, использует алиасы. |
| `mixed` | UUID → direct, алиасы → catalog. | Рекомендуемый default. |

### Как работает catalog

1. Worker знает о ботах и чатах (из конфига)
2. Worker периодически публикует routing catalog в отдельную queue/topic
3. Producer подписывается на catalog и кэширует его локально
4. При отправке по алиасу — producer резолвит UUID из кэша

## Использование

### CLI

```bash
# Direct mode — по UUID
express-botx enqueue --bot-id BOT-UUID --chat-id CHAT-UUID "Hello"

# Catalog mode — по алиасам
express-botx enqueue --routing-mode catalog --bot alerts --chat-id deploy "Deploy OK"

# Mixed mode (default)
express-botx enqueue --chat-id deploy "Hello"
```

### HTTP-сервер

```bash
# Запуск в async-режиме
express-botx serve --enqueue --config config.yaml
```

Ответ — `202 Accepted`:

```json
{"ok": true, "queued": true, "request_id": "0d6d7f87-0a2f-4c5b-b0d4-4d0b705a77e2"}
```

HTTP payload расширяется полями для direct routing:

```json
{"routing_mode": "direct", "bot_id": "bot-uuid", "chat_id": "chat-uuid", "message": "deploy ok"}
```

### Worker

```bash
# Запуск
express-botx worker --config config.yaml

# С health check
express-botx worker --config config.yaml --health-listen :8081

# Без публикации каталога
express-botx worker --config config.yaml --no-catalog-publish
```

## Health check (worker)

При `--health-listen` worker поднимает HTTP-сервер:

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/healthz` | 200 если consumer подключён к брокеру, 503 иначе |
| `GET` | `/readyz` | 200 когда worker готов, 503 при startup/shutdown |

## Docker Compose примеры

Готовые конфигурации для локальной разработки:

- `examples/docker-compose-kafka/` — express-botx + Kafka + Zookeeper
- `examples/docker-compose-rabbitmq/` — express-botx + RabbitMQ

## Деплой в Kubernetes

Для async-режима рекомендуется два отдельных Deployment:

```bash
# API-сервер (HTTP → очередь)
helm install api oci://ghcr.io/lavr/charts/express-botx -f values-api.yaml

# Worker (очередь → BotX API)
helm install worker oci://ghcr.io/lavr/charts/express-botx -f values-worker.yaml
```

Подробнее о values: [charts/express-botx/README.md](../charts/express-botx/README.md)
