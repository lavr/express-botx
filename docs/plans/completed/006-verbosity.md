# RFC-006: Уровни логирования (-v)

- **Статус:** Draft
- **Дата:** 2026-03-13

## Контекст

Сейчас express-botx не выводит отладочную информацию. При проблемах с подключением, аутентификацией или отправкой сообщений приходится угадывать причину по короткому сообщению об ошибке.

## Предложение

Глобальный флаг `-v` с тремя уровнями:

| Уровень | Флаг | Что выводится |
|---------|------|---------------|
| 0 (silent) | — | Только результат и ошибки |
| 1 (verbose) | `-v` | + ключевые шаги выполнения |
| 2 (debug) | `-vv` | + HTTP-запросы/ответы, заголовки |
| 3 (trace) | `-vvv` | + тела запросов/ответов, секреты замаскированы |

### Уровень 0 (по умолчанию)

Как сейчас — только результат или ошибка:

```
Bot added: bot1 (express.invitro.ru, 9e944012-...)
```

```
error: sending: send failed: HTTP 403
```

### Уровень 1 (-v)

Ключевые шаги — что происходит и с какими параметрами:

```
config: loaded from ./express-botx.yaml
config: using bot "prod" (express.invitro.ru)
auth: token loaded from cache (expires in 2847s)
send: POST /api/v4/botx/notifications/direct -> 202 Accepted
Bot added: bot1 (express.invitro.ru, 9e944012-...)
```

Для `serve`:

```
config: loaded from ./express-botx.yaml
config: using bot "prod" (express.invitro.ru)
auth: token obtained (new)
server: listening on :8080 (base_path: /api/v1)
server: 2 API keys loaded
server: POST /api/v1/send [key: monitoring] -> 200 (143ms)
server: POST /api/v1/send [key: ci] -> 502 (5012ms)
```

### Уровень 2 (-vv)

HTTP-детали — URL, метод, заголовки (без секретов), статус, время:

```
config: loaded from ./express-botx.yaml
config: using bot "prod" (express.invitro.ru)
auth: GET https://express.invitro.ru/api/v2/botx/bots/9e944012-.../token?signature=***
auth: <- 200 OK (87ms)
auth: token cached (ttl: 3600s)
send: POST https://express.invitro.ru/api/v4/botx/notifications/direct
send: -> Content-Type: application/json
send: -> Authorization: Bearer ***
send: <- 202 Accepted (143ms)
```

### Уровень 3 (-vvv)

Полные тела запросов и ответов. Секреты маскируются:

```
auth: GET https://express.invitro.ru/api/v2/botx/bots/9e944012-.../token?signature=A1B2***F6
auth: <- 200 OK (87ms)
auth: <- {"status":"ok","result":"eyJ***..."}
send: POST https://express.invitro.ru/api/v4/botx/notifications/direct
send: -> {"group_chat_id":"7ee8aaa9-...","notification":{"status":"ok","body":"hello"}}
send: <- 202 Accepted (143ms)
send: <- {"status":"ok","result":{"sync_id":"ef051dea-..."}}
```

## Реализация

### Флаг

Парсинг `-v`, `-vv`, `-vvv` вручную (стандартный `flag` не поддерживает повторяющиеся флаги):

```go
// В globalFlags или до парсинга
verbosity := countVerboseFlags(args) // считает -v, -vv, -vvv
```

Альтернатива — числовой флаг `--verbose N`:

```
--verbose 0   (silent, default)
--verbose 1   (= -v)
--verbose 2   (= -vv)
--verbose 3   (= -vvv)
```

Поддерживаются оба формата. `-v` удобнее для CLI, `--verbose N` — для конфигов и скриптов.

Переменная окружения: `EXPRESS_VERBOSE=2`.

### Логгер

Новый пакет `internal/log`:

```go
package log

var Level int // 0-3

func V(level int, format string, args ...any)  // выводит если Level >= level
func V1(format string, args ...any)            // V(1, ...)
func V2(format string, args ...any)            // V(2, ...)
func V3(format string, args ...any)            // V(3, ...)
```

Вывод идёт в stderr (чтобы не мешать stdout pipe/JSON).

### Маскирование секретов

На уровнях 2-3 автоматическая маскировка:
- Bearer токены: `Bearer eyJ***`
- Signature: `A1B2***F6` (первые 4 + последние 2 символа)
- Secret в конфиге: `****`

### Что логируется по уровням

| Компонент | Уровень 1 | Уровень 2 | Уровень 3 |
|-----------|-----------|-----------|-----------|
| config | файл, выбранный бот | + все слои (yaml/env/flag) | + значения полей |
| auth | cache hit/miss, token obtained | + HTTP метод/URL/статус | + тела |
| botapi (send) | метод + статус | + заголовки | + тела запросов/ответов |
| botapi (chats, users) | метод + статус | + заголовки | + тела |
| server | запрос + ключ + статус + время | + заголовки клиента | + тела |

## Изменения

| Действие | Файл |
|----------|------|
| NEW | `internal/log/log.go` |
| MODIFY | `internal/cmd/cmd.go` — парсинг `-v`/`--verbose`, установка `log.Level` |
| MODIFY | `internal/config/config.go` — логирование при загрузке (V1, V2) |
| MODIFY | `internal/auth/auth.go` — логирование запроса токена |
| MODIFY | `internal/botapi/client.go` — логирование HTTP-вызовов |
| MODIFY | `internal/server/server.go` — расширенное логирование запросов |
| MODIFY | `internal/token/cache.go` — логирование cache hit/miss |

## Что НЕ включаем

- Запись логов в файл — перенаправление stderr достаточно (`2>debug.log`)
- Структурированные логи (JSON) — overkill для CLI-утилиты
- Per-компонент уровни (только auth, только send) — избыточно
