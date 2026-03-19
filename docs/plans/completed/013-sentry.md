# RFC-013: Интеграция с Sentry (error tracking)

- **Статус:** Implemented
- **Дата:** 2026-03-14

## Контекст

Sentry — инструмент для отслеживания ошибок и исключений. В отличие от APM (New Relic, Elastic APM), Sentry фокусируется на:

- Перехвате паников в HTTP-хендлерах
- Сборе и группировке ошибок
- Контексте ошибок (URL, метод, заголовки, стек вызовов)

APM и error tracking — ортогональные механизмы. Можно использовать оба одновременно (например, New Relic для метрик + Sentry для ошибок).

## Решение

Отдельный пакет `internal/errtrack/` с интерфейсом `Tracker`, по аналогии с `internal/apm/`:

- `errtrack.Tracker` — интерфейс: `Middleware()`, `CaptureError()`, `Flush()`
- `noop.go` (`//go:build !sentry`) — noop по умолчанию
- `sentry.go` (`//go:build sentry`) — реализация через `sentry-go`

APM (`internal/apm/`) остаётся без изменений.

### Сборка

```bash
# Без Sentry (по умолчанию)
go build .

# С Sentry
go build -tags sentry .

# С Sentry + APM
go build -tags "sentry newrelic" .
```

### Конфигурация

Через стандартные переменные окружения Sentry SDK:

| Переменная | Описание | Обязательно |
|---|---|---|
| `SENTRY_DSN` | DSN проекта | да |
| `SENTRY_ENVIRONMENT` | Окружение (prod, staging) | нет |
| `SENTRY_RELEASE` | Версия приложения | нет |

Если `SENTRY_DSN` пуст или инициализация не удалась — используется noop.

### Kubernetes / Helm

```yaml
extraEnv:
  - name: SENTRY_DSN
    valueFrom:
      secretKeyRef:
        name: sentry
        key: dsn
  - name: SENTRY_ENVIRONMENT
    value: production
```

## Архитектура

```
internal/errtrack/
  errtrack.go    # интерфейс Tracker
  noop.go        # //go:build !sentry — noop
  sentry.go      # //go:build sentry — Sentry SDK

internal/apm/    # без изменений
  apm.go         # интерфейс Provider (трассировка запросов)
  noop.go
  newrelic.go
  elastic.go
```

Интеграция в сервер:

```
HTTP request
  → errtrack.Middleware (перехват паников)
    → apm.WrapHandler (трассировка)
      → authMiddleware (авторизация)
        → handler (бизнес-логика)
```

### Интерфейс

```go
type Tracker interface {
    Middleware(h http.Handler) http.Handler  // оборачивает mux для перехвата паников
    CaptureError(err error)                  // отправляет ошибку в Sentry
    Flush()                                  // ждёт отправки pending-событий
}
```

## CI

Бинарники и Docker-образ собираются с `-tags sentry`:

```yaml
# Бинарники
go build -tags sentry -ldflags="..." .

# Docker (Dockerfile ARG BUILD_TAGS="sentry")
docker build --build-arg BUILD_TAGS="sentry" --build-arg APM_TAG=newrelic .
```

## Что НЕ включаем

- Performance monitoring через Sentry (это задача APM)
- Отправку логов в Sentry — логирование остаётся в stderr
- Sentry breadcrumbs — достаточно автоматических из HTTP middleware
- User feedback API — не применимо для server-to-server
