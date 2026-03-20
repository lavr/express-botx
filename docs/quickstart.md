# Quickstart

Примеры настройки и использования express-botx для типичных сценариев.

Замените значения `host`, `bot-id`, `secret` и UUID чатов на свои.

---

## Кейс 1: Один бот, один чат

### Настройка через CLI

```bash
# 1. Добавляем бота (secret обменяется на токен и токен запишется в конфиг)
express-botx config bot add \
  --name mybot \
  --host express.company.ru \
  --bot-id 11111111-1111-1111-1111-111111111111 \
  --secret my-bot-secret

# 2. Добавляем чат
express-botx config chat add \
  --chat-id aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa \
  --alias alerts 

# 3. Добавляем API-ключ для HTTP-сервера (ключ сгенерируется автоматически)
express-botx config apikey add --name monitoring

# Проверяем результат
express-botx config show
```


### Отправка из CLI

Теперь можно отправить тестовое сообщение:

```bash
# Простая отправка сообщение - будет использован единственный бот и единственный чат
express-botx send "Тестовое сообщение"

# Можно указать статус
express-botx send "Сборка упала" --status error

# И можно отправить файл
express-botx send --file report.txt "Отчёт прикреплён"
```

### HTTP-сервер

С этим же конфигом можно запустить http-сервер:

```bash
express-botx serve
```

И отправить сообщение:

```bash
curl -X POST http://localhost:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "Деплой прошёл"
  }'
```

Для alertmanage есть спецаильный url - пропиши его в свой алертменеджер или проверь локально.
Сообщение придет в единственный чат:

```bash
curl -X POST http://localhost:8080/api/v1/alertmanager \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "firing",
    "alerts": [
      {
        "status": "firing",
        "labels": {
          "alertname": "HighCPU",
          "severity": "critical",
          "instance": "server1:9090"
        },
        "annotations": {
          "summary": "CPU usage is above 90%"
        },
        "startsAt": "2026-01-01T00:00:00Z",
        "endsAt": "0001-01-01T00:00:00Z"
      }
    ]
  }'
```

Для grafana тоже есть спецаильный url - пропиши его в свою графану или проверь локально:

```bash
curl -X POST http://localhost:8080/api/v1/grafana \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "firing",
    "alerts": [
      {
        "status": "firing",
        "labels": {
          "alertname": "DiskFull"
        },
        "annotations": {
          "summary": "Disk usage above 90%"
        },
        "startsAt": "2026-01-01T00:00:00Z",
        "dashboardURL": "https://grafana.company.ru/d/abc123"
      }
    ]
  }'
```

---

## Кейс 2: Один бот, несколько чатов

Отличие от кейса 1 — добавляем несколько чатов.

### Настройка

```bash
# Бот уже добавлен (см. кейс 1), добавляем второй чат
express-botx config chat add \
  --chat-id bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb \
  --alias deploy
```

### CLI

Когда чатов несколько, `--chat-id` обязателен (если не прописан default chat)

```bash
express-botx send --chat-id alerts "Проблема на проде" --status error
express-botx send --chat-id deploy "Деплой v1.2.3 прошёл"
```

### HTTP-сервер

В `/send` указываем `chat_id`:

```bash
curl -X POST http://localhost:8080/api/v1/send \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "chat_id": "deploy",
    "message": "Деплой прошёл"
  }'
```

Для alertmanager и grafana чат можно указать через query-параметр `?chat_id=`:

```bash
# Алерты в чат alerts
curl -X POST "http://localhost:8080/api/v1/alertmanager?chat_id=alerts" \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{...}'

# Графана в чат deploy
curl -X POST "http://localhost:8080/api/v1/grafana?chat_id=deploy" \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{...}'
```

Но если в конфиге прописаны `sever.alertmanager.default_chat_id` / `sever.grafana.default_chat_id` - тогда параметр chat_id в них можно не указывать.

---

## Кейс 3: Два бота, несколько чатов

Разные чаты привязаны к разным ботам. Бот определяется автоматически по привязке чата.

### Настройка

```bash
# Два бота
express-botx config bot add \
  --name bot1 \
  --host express.company.ru \
  --bot-id 11111111-1111-1111-1111-111111111111 \
  --secret bot1-secret

express-botx config bot add \
  --name bot2 \
  --host express.company.ru \
  --bot-id 22222222-2222-2222-2222-222222222222 \
  --secret bot2-secret

# Чаты с привязкой к ботам
express-botx config chat add \
  --chat-id aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa \
  --alias alerts-project1 \
  --bot bot1

express-botx config chat add \
  --chat-id cccccccc-cccc-cccc-cccc-cccccccccccc \
  --alias deploy-project1 \
  --bot bot2

# API-ключ
express-botx config apikey add --name monitoring
```

### CLI

Бот подбирается автоматически по привязке чата:

```bash
express-botx send --chat-id alerts-project1 "Алерт" --status error   # → bot1
express-botx send --chat-id deploy-project1 "Деплой прошёл"          # → bot2
```

### HTTP-сервер

В `/send` достаточно указать `chat_id` — бот определится по привязке, аналогично кейсу 2
Для alertmanager и grafana — аналогично кейсу 2, через `?chat_id=`
