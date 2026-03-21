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
