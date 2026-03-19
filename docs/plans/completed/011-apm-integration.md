# RFC-011: Опциональная интеграция APM (New Relic, Elastic APM)

- **Статус:** Draft
- **Дата:** 2026-03-14

## Контекст

Для production-мониторинга HTTP-сервера (`express-botx serve`) нужны APM-метрики: трейсы запросов, время ответа, ошибки. Используются два APM-провайдера: New Relic и Elastic APM.

Требования:
- APM-зависимость **не должна** попадать в бинарник по умолчанию
- Выбор провайдера — на этапе сборки через build tags
- Конфигурация — через переменные окружения (стандарт обоих SDK)
- Код сервера не должен знать о конкретном провайдере

## Предложение

Пакет `internal/apm` с интерфейсом и тремя реализациями, выбираемыми через build tags.

New Relic и Elastic APM **взаимоисключающие** — в одной сборке может быть только один провайдер (или ни одного). Build tags `newrelic` и `elasticapm` нельзя указывать одновременно — компиляция завершится ошибкой из-за дублирования функции `New()`.

### Build tags

```bash
# Без APM (по умолчанию) — noop, нулевой overhead
go build -o express-botx .

# С New Relic (нельзя совмещать с elasticapm)
go build -tags newrelic -o express-botx .

# С Elastic APM (нельзя совмещать с newrelic)
go build -tags elasticapm -o express-botx .
```

### Интерфейс

```go
// internal/apm/apm.go
package apm

import "net/http"

// Provider — APM-провайдер. Реализация выбирается build tag.
type Provider interface {
    // WrapHandler оборачивает HTTP handler для трейсинга.
    WrapHandler(pattern string, h http.Handler) http.Handler
    // Shutdown завершает работу агента, отправляет буфер.
    Shutdown()
}
```

### Реализация: noop (по умолчанию)

```go
// internal/apm/noop.go
//go:build !newrelic && !elasticapm

package apm

import "net/http"

type noopProvider struct{}

func New(appName string) Provider { return noopProvider{} }

func (noopProvider) WrapHandler(_ string, h http.Handler) http.Handler { return h }
func (noopProvider) Shutdown()                                          {}
```

### Реализация: New Relic

```go
// internal/apm/newrelic.go
//go:build newrelic

package apm

import (
    "net/http"
    "time"

    "github.com/newrelic/go-agent/v3/newrelic"
)

type nrProvider struct {
    app *newrelic.Application
}

func New(appName string) Provider {
    app, err := newrelic.NewApplication(
        newrelic.ConfigAppName(appName),
        newrelic.ConfigFromEnvironment(), // NEW_RELIC_LICENSE_KEY, etc.
    )
    if err != nil {
        return noopFallback{}
    }
    return &nrProvider{app: app}
}

func (p *nrProvider) WrapHandler(pattern string, h http.Handler) http.Handler {
    _, wrapped := newrelic.WrapHandle(p.app, pattern, h)
    return wrapped
}

func (p *nrProvider) Shutdown() {
    p.app.Shutdown(5 * time.Second)
}

type noopFallback struct{}

func (noopFallback) WrapHandler(_ string, h http.Handler) http.Handler { return h }
func (noopFallback) Shutdown()                                          {}
```

Конфигурация через стандартные переменные New Relic:
- `NEW_RELIC_LICENSE_KEY` — ключ (обязателен)
- `NEW_RELIC_APP_NAME` — переопределяет имя приложения
- `NEW_RELIC_ENABLED` — `true`/`false`

### Реализация: Elastic APM

```go
// internal/apm/elastic.go
//go:build elasticapm

package apm

import (
    "net/http"

    "go.elastic.co/apm/v2"
    "go.elastic.co/apm/module/apmhttp/v2"
)

type elasticProvider struct {
    tracer *apm.Tracer
}

func New(appName string) Provider {
    tracer, err := apm.NewTracerOptions(apm.TracerOptions{
        ServiceName: appName,
    })
    if err != nil {
        return noopFallback{}
    }
    return &elasticProvider{tracer: tracer}
}

func (p *elasticProvider) WrapHandler(pattern string, h http.Handler) http.Handler {
    return apmhttp.Wrap(h,
        apmhttp.WithTracer(p.tracer),
        apmhttp.WithServerRequestName(func(r *http.Request) string { return pattern }),
    )
}

func (p *elasticProvider) Shutdown() {
    p.tracer.Flush(nil)
    p.tracer.Close()
}

type noopFallback struct{}

func (noopFallback) WrapHandler(_ string, h http.Handler) http.Handler { return h }
func (noopFallback) Shutdown()                                          {}
```

Конфигурация через стандартные переменные Elastic APM:
- `ELASTIC_APM_SERVER_URL` — URL APM Server (обязателен)
- `ELASTIC_APM_SECRET_TOKEN` или `ELASTIC_APM_API_KEY` — аутентификация
- `ELASTIC_APM_SERVICE_NAME` — переопределяет имя
- `ELASTIC_APM_ENVIRONMENT` — `production`/`staging`

### Интеграция в сервер

В `internal/cmd/serve.go`:

```go
import "github.com/lavr/express-botx/internal/apm"

func runServe(args []string, deps Deps) error {
    // ...
    provider := apm.New("express-botx")
    defer provider.Shutdown()

    // передать в server
    srv := server.New(cfg, client, provider)
    // ...
}
```

В `internal/server/server.go` — при регистрации роутов:

```go
type Server struct {
    // ...
    apm apm.Provider
}

func (s *Server) routes() http.Handler {
    mux := http.NewServeMux()
    mux.Handle("/healthz", s.handleHealthz())                              // без APM
    mux.Handle(basePath+"/send", s.apm.WrapHandler("POST /send", s.authMiddleware(s.handleSend())))
    mux.Handle(basePath+"/alertmanager", s.apm.WrapHandler("POST /alertmanager", s.authMiddleware(s.handleAlertmanager())))
    mux.Handle(basePath+"/grafana", s.apm.WrapHandler("POST /grafana", s.authMiddleware(s.handleGrafana())))
    return mux
}
```

### Helm chart

APM включается через `extraEnv` в values — никаких изменений в чарте:

```yaml
# New Relic
extraEnv:
  - name: NEW_RELIC_LICENSE_KEY
    valueFrom:
      secretKeyRef:
        name: newrelic-secret
        key: license-key
  - name: NEW_RELIC_APP_NAME
    value: "express-botx-prod"

# Elastic APM
extraEnv:
  - name: ELASTIC_APM_SERVER_URL
    value: "https://apm.example.com:8200"
  - name: ELASTIC_APM_SECRET_TOKEN
    valueFrom:
      secretKeyRef:
        name: elastic-apm-secret
        key: token
```

Но Docker-образ должен быть собран с соответствующим тегом.

### Dockerfile

Добавить build arg для выбора APM:

```dockerfile
ARG APM_TAG=""
RUN go build -tags "${APM_TAG}" -ldflags="-s -w -X main.version=${VERSION}" -o express-botx .
```

Сборка:
```bash
# Без APM
docker build .

# С New Relic
docker build --build-arg APM_TAG=newrelic .

# С Elastic APM
docker build --build-arg APM_TAG=elasticapm .
```

### CI: дополнительные Docker-образы (опционально)

В `release.yml` можно добавить matrix для сборки вариантов:

```yaml
strategy:
  matrix:
    include:
      - tag_suffix: ""
        apm_tag: ""
      - tag_suffix: "-newrelic"
        apm_tag: "newrelic"
      - tag_suffix: "-elasticapm"
        apm_tag: "elasticapm"
```

Результат: `lavr/express-botx:0.8.0`, `lavr/express-botx:0.8.0-newrelic`, `lavr/express-botx:0.8.0-elasticapm`.

## Файлы

| Действие | Файл |
|----------|------|
| CREATE | `internal/apm/apm.go` — интерфейс `Provider` |
| CREATE | `internal/apm/noop.go` — noop-реализация (без тега) |
| CREATE | `internal/apm/newrelic.go` — New Relic (`//go:build newrelic`) |
| CREATE | `internal/apm/elastic.go` — Elastic APM (`//go:build elasticapm`) |
| MODIFY | `internal/server/server.go` — принимать `apm.Provider`, оборачивать handlers |
| MODIFY | `internal/cmd/serve.go` — создавать `apm.New()`, передавать в сервер |
| MODIFY | `Dockerfile` — `ARG APM_TAG` |

## Проверка

1. `go build -o express-botx .` — собирается без APM-зависимостей
2. `go build -tags newrelic -o express-botx .` — собирается с New Relic
3. `go build -tags elasticapm -o express-botx .` — собирается с Elastic APM
4. `go vet ./...` — без ошибок для всех вариантов
5. Запуск без env-переменных APM — сервер работает, noop
6. Запуск с `NEW_RELIC_LICENSE_KEY` — трейсы появляются в New Relic
7. Запуск с `ELASTIC_APM_SERVER_URL` — трейсы появляются в Elastic APM
8. `/healthz` — не обёрнут в APM (не спамит трейсами)
