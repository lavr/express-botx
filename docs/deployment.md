# Деплой

Варианты запуска express-botx в production.

## Бинарник + systemd

Самый простой способ запустить HTTP-сервер на Linux-хосте.

### Установка

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
curl -sL "https://github.com/lavr/express-botx/releases/latest/download/express-botx-${OS}-${ARCH}.tar.gz" | tar xz
sudo mv express-botx /usr/local/bin/
sudo chmod +x /usr/local/bin/express-botx
```

### Конфиг

```bash
sudo mkdir -p /etc/express-botx
sudo vim /etc/express-botx/config.yaml
sudo chmod 600 /etc/express-botx/config.yaml
```

### Пользователь

```bash
sudo useradd -r -s /usr/sbin/nologin express-botx
sudo chown express-botx:express-botx /etc/express-botx/config.yaml
```

### Unit-файл

```ini
# /etc/systemd/system/express-botx.service
[Unit]
Description=express-botx HTTP server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=express-botx
Group=express-botx
ExecStart=/usr/local/bin/express-botx serve --config /etc/express-botx/config.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadOnlyPaths=/etc/express-botx

[Install]
WantedBy=multi-user.target
```

### Запуск

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now express-botx
sudo systemctl status express-botx
sudo journalctl -u express-botx -f
```

---

## Docker

### Отправка сообщений

```bash
# С флагами
docker run --rm lavr/express-botx send \
  --host express.company.ru --bot-id UUID --secret KEY \
  --chat-id UUID "Hello from Docker"

# С конфигом
docker run --rm -v ./config.yaml:/config.yaml lavr/express-botx \
  send --config /config.yaml "Hello"
```

### HTTP-сервер

```bash
docker run -d \
  --name express-botx \
  --restart unless-stopped \
  -p 8080:8080 \
  -v ./config.yaml:/config.yaml:ro \
  lavr/express-botx serve --config /config.yaml
```

### Worker

```bash
docker run -d \
  --name express-botx-worker \
  --restart unless-stopped \
  -v ./config.yaml:/config.yaml:ro \
  lavr/express-botx worker --config /config.yaml --health-listen :8081
```

### Сборка с поддержкой очередей

Публичный образ `lavr/express-botx` уже собран с поддержкой RabbitMQ и Kafka, поэтому `enqueue` и `worker` можно запускать без пересборки. Отдельная сборка нужна только если вы хотите свой набор build tags или кастомный образ:

```bash
# С RabbitMQ
docker build --build-arg BUILD_TAGS="sentry rabbitmq" -t express-botx:rabbitmq .

# С Kafka
docker build --build-arg BUILD_TAGS="sentry kafka" -t express-botx:kafka .

# С обоими драйверами
docker build --build-arg BUILD_TAGS="sentry rabbitmq kafka" -t express-botx:full .
```

---

## Docker Compose

В директории `examples/` есть готовые конфигурации:

- `examples/docker-compose-kafka/` — express-botx + Kafka
- `examples/docker-compose-rabbitmq/` — express-botx + RabbitMQ

### Минимальный docker-compose.yml

```yaml
services:
  express-botx:
    image: lavr/express-botx
    command: serve --config /config.yaml
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/config.yaml:ro
```

---

## Kubernetes (Helm)

### Установка

```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx -f values.yaml
```

Или из исходников:

```bash
helm install express-botx ./charts/express-botx -f values.yaml
```

### Минимальный values.yaml

```yaml
config:
  bots:
    prod:
      host: express.company.ru
      id: "bot-uuid"
      secret: "bot-secret"
  chats:
    alerts: "chat-uuid"
  server:
    listen: ":8080"
    base_path: /api/v1
    api_keys:
      - name: monitoring
        key: "api-key"
    alertmanager:
      default_chat_id: alerts
    grafana:
      default_chat_id: alerts

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

Конфиг монтируется из Kubernetes **Secret** (не ConfigMap), т.к. содержит bot secret и API-ключи.

Для использования существующего секрета: `existingSecret: my-secret`.

### HTTPS до pod

`ingress.tls` настраивает клиент → Ingress, а `tls.enabled` — HTTPS для
Ingress и kubelet probes → pod. При pod-level TLS HTTPS заменяет HTTP на том же
адресе `config.server.listen`; отдельный listener не создаётся. Нужно согласовать
`containerPort`, `service.targetPort` и фактический порт `config.server.listen`.

Пример с Certificate от cert-manager:

```yaml
containerPort: 8443
service:
  targetPort: 8443
config:
  server:
    listen: ":8443"
tls:
  enabled: true
  reloadInterval: 60s
  certManager:
    enabled: true
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer
    dnsNames:
      - botx.example.com
```

Вместо cert-manager можно использовать существующий Secret. В нём обязательны
точные ключи `tls.crt` и `tls.key`:

```bash
kubectl create secret tls express-botx-tls \
  --cert=cert.pem \
  --key=key.pem
```

Флаги `--cert` и `--key` принимают произвольные локальные имена файлов; команда
выше всё равно создаёт в Secret стандартные ключи `tls.crt` и `tls.key`.

```yaml
containerPort: 8443
service:
  targetPort: 8443
config:
  server:
    listen: ":8443"
tls:
  enabled: true
  existingSecret: express-botx-tls
```

Уже существующий Secret, ключи которого сами называются `cert.pem`/`key.pem`,
не поддерживается: kubelet не сможет смонтировать указанные chart-ом
`tls.crt`/`tls.key`, поэтому контейнер не стартует. Helm не может проверить
содержимое внешнего Secret при offline-render. Источники cert-manager и
`tls.existingSecret` взаимоисключающие, а `tls.enabled` запрещён для
`mode: worker`.

Если включён Ingress, его controller должен подключаться к Service по HTTPS.
Chart не добавляет controller-specific аннотации автоматически. Например,
для ingress-nginx:

```yaml
ingress:
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
```

Эта backend-аннотация не заменяет `ingress.tls`: тот отдельно настраивает TLS
на участке клиент → Ingress.

### Дополнительные переменные окружения

Через `extraEnv` можно передавать переменные окружения в контейнер. Значения поддерживают Go-шаблоны Helm:

```yaml
extraEnv:
  - name: NEW_RELIC_APP_NAME
    value: '{{ .Release.Namespace }}/{{ .Release.Name }}'
  - name: MY_VAR
    value: "static-value"
  - name: VAULT_TOKEN
    valueFrom:
      secretKeyRef:
        name: vault-token
        key: token
```

### Async-режим (два Deployment)

Для `serve --enqueue` + `worker` рекомендуется два отдельных Deployment:

```bash
# API-сервер (HTTP → очередь)
helm install api oci://ghcr.io/lavr/charts/express-botx -f values-api.yaml

# Worker (очередь → BotX API)
helm install worker oci://ghcr.io/lavr/charts/express-botx -f values-worker.yaml
```

Подробнее о values для async-режима: [charts/express-botx/README.md](../charts/express-botx/README.md)

---

## Метрики Prometheus

Сервер (`serve` и `serve --enqueue`) может отдавать метрики в формате
Prometheus. По умолчанию они выключены.

```yaml
server:
  metrics:
    enabled: true
    listen: "127.0.0.1:9090"   # обязателен; в контейнере обычно ":9090"
    path: /metrics             # по умолчанию /metrics; только этот путь, без вложенных
```

Или переменной окружения `EXPRESS_BOTX_SERVER_METRICS_LISTEN=:9090`: она
включает метрики на этом адресе, даже если в конфиге `enabled: false`.

Метрики отдаются на **отдельном** адресе, всегда по HTTP, без `base_path` и без
аутентификации. Кроме `path`, на этом адресе ничего нет. Открывайте его только
сборщику метрик: в Kubernetes — порт пода без Service наружу и без Ingress. По
этой же причине у `listen` нет значения по умолчанию: слушать все интерфейсы
нужно решить явно. Если адрес занят, сервер не запустится.

Что есть (префикс `express_botx_`):

| Метрика | Метки | Что считает |
|---|---|---|
| `http_requests_total` | `method`, `handler`, `code` | Запросы к серверу. `handler` — шаблон маршрута (`/api/v1/send`), неизвестный путь — `other` |
| `http_request_duration_seconds` | `method`, `handler` | Время ответа сервера |
| `deliveries_total` | `handler`, `outcome` | Исход по каждому чату: `delivered`, `queued`, `failed` |
| `partial_deliveries_total` | `handler` | Ответ успешный, но часть чатов не получила сообщение |
| `edits_total` | `handler`, `outcome` | Редактирование постов Mattermost |
| `botx_requests_total` | `endpoint`, `code` | HTTP-попытки к BotX, включая получение токена и повторы. `code` — статус или `error`. `2xx` значит, что BotX ответил, а не что ответ годный |
| `botx_request_duration_seconds` | `endpoint` | Время до заголовков ответа BotX |
| `membership_checks_total` | `outcome`, `source` | Проверки членства для ключей с `chats_where_member`: `member`, `not_member`, `error`; `source` — `cache` или `lookup` |
| `membership_lookups_total` | `outcome` | Запросы членства, действительно ушедшие в BotX |
| `membership_lookups_inflight` | — | Сколько таких запросов идёт сейчас |
| `membership_lookups_rejected_total` | — | Проверки, отклонённые по лимиту одновременных запросов (ответ `503`) |
| `queue_publish_total` | `outcome` | Публикации в очередь в режиме `--enqueue`: `ok`, `error` |
| `build_info` | `version`, `goversion`, `mode` | Всегда `1`; версия, версия Go и режим (`sync` или `enqueue`) |

Плюс стандартные `go_*` и `process_*`.

В метках нет чатов, ключей, ботов, текста сообщений и строк запроса. Основные
серии видны с нулём сразу после старта, поэтому `rate()` по ним не пропадает.
Метрик `worker` пока нет.

В Helm-чарте метрики включаются `metrics.enabled: true`, ServiceMonitor —
`metrics.serviceMonitor.enabled: true` (см. README чарта).

Инстанс метками не помечается: это делает Prometheus при сборе (`instance`, а
через Kubernetes service discovery — `pod`, `namespace`). Версию на каждом
поде показывает `build_info`.

`deliveries_total` считает только чаты, до которых дошла отправка. Запрос,
отклонённый целиком до неё (`400`, `401`, `403`, `503` проверки членства),
туда не попадает: его видно в `http_requests_total` и `membership_checks_total`.

Пример алерта на долю неудачных доставок:

```promql
sum(rate(express_botx_deliveries_total{outcome="failed"}[5m]))
  / sum(rate(express_botx_deliveries_total[5m])) > 0.1
```

## Обратный прокси

### nginx

```nginx
upstream express-botx {
    server 127.0.0.1:8080;
}

server {
    listen 443 ssl;
    server_name express-botx.company.ru;

    ssl_certificate     /etc/ssl/certs/express-botx.pem;
    ssl_certificate_key /etc/ssl/private/express-botx.key;

    location / {
        proxy_pass http://express-botx;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```
