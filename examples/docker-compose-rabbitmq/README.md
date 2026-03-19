# express-botx + RabbitMQ

Пример запуска express-botx в асинхронном режиме с RabbitMQ.

## Архитектура

```
HTTP-клиент → producer (serve --enqueue) → RabbitMQ → worker → BotX API
```

- **Producer** — HTTP-сервер, принимает запросы и публикует сообщения в очередь
- **Worker** — потребитель очереди, аутентифицируется в BotX API и отправляет сообщения
- **RabbitMQ** — брокер сообщений

## Запуск

```bash
# 1. Скопируйте и заполните .env
cp .env.example .env
# Укажите BOT_HOST, BOT_ID, BOT_SECRET и CHAT_ID

# 2. Запустите
docker compose up --build
```

## Проверка

При запуске producer генерирует API-ключ и выводит его в лог. Найдите его:

```bash
docker compose logs producer | grep "generated key"
```

Отправьте сообщение (подставьте свои BOT_ID, CHAT_ID и API-ключ):

```bash
curl -X POST http://localhost:8080/api/v1/send \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <ключ из лога>" \
  -d '{
    "bot_id": "<BOT_ID>",
    "chat_id": "<CHAT_ID>",
    "message": "Hello from async mode!"
  }'

# Ответ: {"ok":true,"queued":true,"request_id":"..."}
```

## Мониторинг

- RabbitMQ Management UI: http://localhost:15672 (guest/guest)
- Worker health check: http://localhost:9090/healthz (внутри Docker-сети)
