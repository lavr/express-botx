# express-botx + Kafka

Пример запуска express-botx в асинхронном режиме с Apache Kafka (KRaft, без Zookeeper).

## Архитектура

```
HTTP-клиент → producer (serve --enqueue) → Kafka → worker → BotX API
```

- **Producer** — HTTP-сервер, принимает запросы и публикует сообщения в топик
- **Worker** — consumer group, читает топик, аутентифицируется в BotX API и отправляет сообщения
- **Kafka** — брокер сообщений (KRaft mode, без Zookeeper)

## Запуск

```bash
# 1. Скопируйте и заполните .env
cp .env.example .env
# Укажите BOT_HOST, BOT_ID, BOT_SECRET и CHAT_ID

# 2. Запустите
docker compose up --build
```

## Масштабирование воркеров

Kafka позволяет запускать несколько воркеров в одной consumer group:

```bash
docker compose up --build --scale worker=3
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
