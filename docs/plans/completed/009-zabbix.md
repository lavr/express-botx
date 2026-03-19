# RFC-009: Интеграция с Zabbix

- **Статус:** Accepted
- **Дата:** 2026-03-13

## Контекст

Zabbix — распространённая система мониторинга. Для отправки уведомлений он использует **Media Types** — механизм, позволяющий настроить способ доставки (email, SMS, webhook, скрипт).

Начиная с Zabbix 4.4 появился тип **Webhook** — HTTP-запрос с JavaScript-обработкой на стороне Zabbix. В отличие от Alertmanager и Grafana, у Zabbix **нет фиксированного формата webhook-payload**. JavaScript-код внутри Media Type полностью определяет формат запроса.

## Решение: отдельный эндпоинт НЕ нужен

Для Alertmanager и Grafana отдельные эндпоинты (`/alertmanager`, `/grafana`) оправданы — у них фиксированный JSON-формат, который нужно парсить и преобразовывать.

Zabbix же сам формирует запрос произвольного формата. Это значит, что JavaScript в Media Type может сразу формировать payload в формате `POST /api/v1/send`, который express-botx уже поддерживает.

Дополнительный эндпоинт создал бы лишний код без реальной пользы.

## Варианты интеграции

### Вариант 1: Webhook Media Type → `/api/v1/send` (рекомендуемый)

Zabbix Webhook Media Type отправляет JSON напрямую в существующий эндпоинт `/api/v1/send`.

### Вариант 2: Webhook Media Type → `/api/v1/alertmanager`

Если в инфраструктуре уже настроен alertmanager-эндпоинт и хочется единый формат — можно формировать payload в формате Alertmanager.

### Вариант 3: Script Media Type

Вызов `express-botx send` как внешнего скрипта. Подходит для legacy-установок без поддержки Webhook Media Type.

## Настройка Zabbix Webhook Media Type

### Шаг 1: Создание Media Type

**Administration → Media types → Create media type**

| Поле | Значение |
|------|----------|
| Name | eXpress Bot |
| Type | Webhook |

**Parameters:**

| Name | Value |
|------|-------|
| `url` | `http://express-botx:8080/api/v1/send` |
| `api_key` | `{$EXPRESS_API_KEY}` |
| `chat_id` | `{ALERT.SENDTO}` |
| `subject` | `{ALERT.SUBJECT}` |
| `message` | `{ALERT.MESSAGE}` |
| `status` | `{TRIGGER.STATUS}` |
| `severity` | `{TRIGGER.NSEVERITY}` |
| `host` | `{HOST.NAME}` |
| `event_id` | `{EVENT.ID}` |

### Шаг 2: JavaScript-код

```javascript
var params = JSON.parse(value);

// Определяем статус сообщения (ok/error)
// TRIGGER.STATUS: PROBLEM или RESOLVED
// TRIGGER.NSEVERITY: 0-5 (Not classified, Information, Warning, Average, High, Disaster)
var msgStatus = "ok";
if (params.status === "PROBLEM" && parseInt(params.severity) >= 3) {
    msgStatus = "error";
}

// Формируем эмодзи по статусу
var emoji = params.status === "PROBLEM" ? "\uD83D\uDD34" : "\u2705";

// Собираем сообщение
var text = emoji + " " + params.subject + "\n\n" + params.message;

var request = new HttpRequest();
request.addHeader("Content-Type: application/json");
request.addHeader("X-API-Key: " + params.api_key);

var payload = JSON.stringify({
    chat_id: params.chat_id,
    message: text,
    status: msgStatus
});

var response = request.post(params.url, payload);
var resp = JSON.parse(response);

if (!resp.ok) {
    throw "express-botx error: " + resp.error;
}

return "OK: sync_id=" + resp.sync_id;
```

### Шаг 3: Тестирование

В интерфейсе Media Type нажать **Test** и указать тестовые значения:

| Parameter | Test value |
|-----------|------------|
| `url` | `http://express-botx:8080/api/v1/send` |
| `api_key` | `your-api-key` |
| `chat_id` | `ops-alerts` |
| `subject` | `PROBLEM: High CPU on web-01` |
| `message` | `CPU usage is 95% for 5 minutes` |
| `status` | `PROBLEM` |
| `severity` | `4` |
| `host` | `web-01` |
| `event_id` | `12345` |

### Шаг 4: Привязка к пользователю

**Administration → Users → (выбрать пользователя) → Media → Add**

| Поле | Значение |
|------|----------|
| Type | eXpress Bot |
| Send to | `ops-alerts` (алиас чата или UUID) |

### Шаг 5: Настройка Action

**Configuration → Actions → Trigger actions → Create action**

**Operations → Add:**

| Поле | Значение |
|------|----------|
| Send to users | (выбрать пользователя) |
| Send only to | eXpress Bot |

## Расширенный пример: разные чаты по severity

```javascript
var params = JSON.parse(value);

var msgStatus = "ok";
var severity = parseInt(params.severity);
if (params.status === "PROBLEM" && severity >= 3) {
    msgStatus = "error";
}

// Severity: 0=Not classified, 1=Information, 2=Warning, 3=Average, 4=High, 5=Disaster
var severityNames = ["Not classified", "Information", "Warning", "Average", "High", "Disaster"];
var severityEmoji = ["\u2139\uFE0F", "\u2139\uFE0F", "\u26A0\uFE0F", "\uD83D\uDFE0", "\uD83D\uDD34", "\uD83D\uDD25"];

var emoji = params.status === "PROBLEM"
    ? severityEmoji[severity] || "\u2753"
    : "\u2705";

var text = emoji + " " + params.subject + "\n"
    + "Host: " + params.host + "\n"
    + "Severity: " + (severityNames[severity] || "Unknown") + "\n"
    + "Event ID: " + params.event_id + "\n\n"
    + params.message;

var request = new HttpRequest();
request.addHeader("Content-Type: application/json");
request.addHeader("X-API-Key: " + params.api_key);

var payload = JSON.stringify({
    chat_id: params.chat_id,
    message: text,
    status: msgStatus
});

var response = request.post(params.url, payload);
var resp = JSON.parse(response);

if (!resp.ok) {
    throw "express-botx error: " + resp.error;
}

return "OK: sync_id=" + resp.sync_id;
```

Для маршрутизации в разные чаты создайте несколько пользователей с разными Media (Send to):

- `zabbix-ops` → Media: eXpress Bot → Send to: `ops-alerts`
- `zabbix-dev` → Media: eXpress Bot → Send to: `dev-alerts`

И в Action выбирайте нужного пользователя в зависимости от условий.

## Пример: Script Media Type (legacy)

Для Zabbix < 4.4 или при предпочтении CLI:

**Скрипт** `/usr/lib/zabbix/alertscripts/express-botx.sh`:

```bash
#!/bin/bash
# $1 = Send to (chat_id), $2 = Subject, $3 = Message
CHAT_ID="$1"
SUBJECT="$2"
MESSAGE="$3"

curl -s -X POST http://express-botx:8080/api/v1/send \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${EXPRESS_API_KEY}" \
  -d "{
    \"chat_id\": \"${CHAT_ID}\",
    \"message\": \"${SUBJECT}\n\n${MESSAGE}\"
  }"
```

Или напрямую через CLI:

```bash
#!/bin/bash
express-botx send --config /etc/express-botx/config.yaml \
  --chat-id "$1" \
  "$2"$'\n\n'"$3"
```

## Что НЕ включаем

- Отдельный эндпоинт `/zabbix` — нет фиксированного формата, JavaScript в Media Type формирует запрос сам
- Импорт Media Type XML/YAML — пользователь создаёт вручную (процедура простая и одноразовая)
- Интеграция с Zabbix API — выходит за рамки express-botx
