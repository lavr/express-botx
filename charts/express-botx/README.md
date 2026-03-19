# express-botx

Helm chart для деплоя [express-botx](https://github.com/lavr/express-botx) — HTTP-сервера для отправки сообщений в корпоративный мессенджер eXpress через BotX API.

Поддерживает вебхуки от Alertmanager и Grafana, а также асинхронную отправку через очередь (RabbitMQ / Kafka).

## Установка

```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx
```

С кастомными значениями:

```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx -f values.yaml
```

## Конфигурация

### Минимальный values.yaml

```yaml
config:
  bots:
    prod:
      host: express.company.ru
      id: "bot-uuid"
      secret: "bot-secret"
  chats:
    ops-alerts: "chat-uuid"
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
```

### Параметры

| Параметр | Описание | По умолчанию |
|----------|----------|--------------|
| `mode` | Режим: `serve`, `serve-enqueue`, `worker` | `serve` |
| `replicaCount` | Количество реплик | `1` |
| `image.repository` | Docker-образ | `lavr/express-botx` |
| `image.tag` | Тег образа | `appVersion` из Chart.yaml |
| `image.pullPolicy` | Pull policy | `IfNotPresent` |
| `config.bots` | Боты (host, id, secret) | `{}` |
| `config.chats` | Алиасы чатов (name → UUID) | `{}` |
| `config.cache.type` | Тип кэша: `file`, `vault`, `none` | `file` |
| `config.cache.ttl` | TTL кэша в секундах | `3600` |
| `config.server.listen` | Адрес для прослушивания | `":8080"` |
| `config.server.base_path` | Базовый путь API | `/api/v1` |
| `config.server.api_keys` | API-ключи (`[{name, key}]`) | `[]` |
| `config.server.alertmanager` | Настройки Alertmanager webhook | не задано |
| `config.server.grafana` | Настройки Grafana webhook | не задано |
| `config.queue.driver` | Драйвер очереди: `rabbitmq`, `kafka` | не задано |
| `config.queue.url` | URL брокера | не задано |
| `config.queue.name` | Имя work queue/topic | не задано |
| `config.queue.group` | Consumer group (worker) | не задано |
| `config.queue.reply_queue` | Reply queue/topic | не задано |
| `config.queue.max_file_size` | Макс. размер файла в async-режиме | `1MB` |
| `config.producer.routing_mode` | Routing mode: `direct`, `catalog`, `mixed` | `mixed` |
| `config.worker.retry_count` | Кол-во попыток при ошибке | `3` |
| `config.worker.retry_backoff` | Базовый backoff | `1s` |
| `config.worker.shutdown_timeout` | Таймаут graceful shutdown | `30s` |
| `config.worker.health_listen` | Адрес health check | `":8081"` |
| `config.catalog.queue_name` | Catalog queue/topic | не задано |
| `config.catalog.cache_file` | Путь к файлу catalog cache | не задано |
| `config.catalog.max_age` | Макс. возраст catalog snapshot | `10m` |
| `config.catalog.publish` | Worker публикует catalog | `true` |
| `config.catalog.publish_interval` | Интервал публикации | `30s` |
| `existingSecret` | Имя существующего Secret с `config.yaml` | `""` |
| `service.type` | Тип сервиса | `ClusterIP` |
| `service.port` | Порт сервиса | `80` |
| `ingress.enabled` | Включить Ingress | `false` |
| `ingress.className` | Ingress class | `""` |
| `ingress.hosts` | Список хостов | `[]` |
| `ingress.tls` | TLS-настройки | `[]` |
| `resources.requests.cpu` | CPU request | `50m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `autoscaling.enabled` | Включить HPA | `false` |
| `extraEnv` | Дополнительные переменные окружения | `[]` |

### Deployment patterns

Для async-режима рекомендуется два отдельных Deployment: API-сервер (`serve --enqueue`) и worker.

API (serve --enqueue):

```yaml
# values-api.yaml
mode: serve-enqueue

config:
  bots: {}          # producer не нужны секреты ботов
  server:
    listen: ":8080"
    api_keys:
      - name: monitoring
        key: "change-me"
  queue:
    driver: kafka
    url: broker:9092
    name: express-botx
  producer:
    routing_mode: mixed
  catalog:
    queue_name: express-botx-catalog
    cache_file: /tmp/express-botx/catalog.json
    max_age: 10m
```

Worker:

```yaml
# values-worker.yaml
mode: worker

config:
  bots:
    alerts:
      host: express.company.ru
      id: "bot-uuid"
      secret: "bot-secret"
  chats:
    deploy: "chat-uuid"
  queue:
    driver: kafka
    url: broker:9092
    name: express-botx
    group: express-botx
  worker:
    health_listen: ":8081"
  catalog:
    queue_name: express-botx-catalog
    publish: true
    publish_interval: 30s
```

Для pure direct mode каталог и `chats:` необязательны — producer'у достаточно `bot_id` и `chat_id`.

```bash
helm install api ./charts/express-botx -f values-api.yaml
helm install worker ./charts/express-botx -f values-worker.yaml
```

### Секреты

Конфиг монтируется из Kubernetes **Secret** (не ConfigMap), т.к. содержит bot secret и API-ключи.

Для использования существующего секрета вместо генерации из `config.*`:

```yaml
existingSecret: my-express-botx-secret
```

Секрет должен содержать ключ `config.yaml` с полным YAML-конфигом.

### Ingress

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
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

### Vault-интеграция

Для чтения секретов из Vault передайте `VAULT_TOKEN` через `extraEnv`:

```yaml
extraEnv:
  - name: VAULT_TOKEN
    valueFrom:
      secretKeyRef:
        name: vault-token
        key: token

config:
  bots:
    prod:
      host: express.company.ru
      id: "bot-uuid"
      secret: "vault:secret/data/express-botx#bot_secret"
```

## Эндпоинты

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/healthz` | Проверка здоровья (используется для probes) |
| `POST` | `{basePath}/send` | Отправка сообщения |
| `POST` | `{basePath}/alertmanager` | Приём вебхуков от Alertmanager |
| `POST` | `{basePath}/grafana` | Приём вебхуков от Grafana |

## Настройка Alertmanager

```yaml
# alertmanager.yml
receivers:
  - name: express
    webhook_configs:
      - url: http://express-botx.express-botx.svc/api/v1/alertmanager
        send_resolved: true
        http_config:
          bearer_token: "<api-key>"
```

## Настройка Grafana

Contact point → Webhook:
- **URL:** `http://express-botx.express-botx.svc/api/v1/grafana`
- **Authorization:** Bearer `<api-key>`
