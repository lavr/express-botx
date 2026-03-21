# RFC-020: Alertmanager и Grafana endpoints включены по умолчанию

- **Статус:** Done
- **Дата:** 2026-03-21

## Контекст

Сейчас endpoints `POST /alertmanager` и `POST /grafana` регистрируются **только** если в конфиге явно присутствует секция `server.alertmanager` / `server.grafana`. Если секция отсутствует — маршрут не регистрируется и запрос возвращает 404.

Это приводит к путанице: пользователь деплоит сервер, настраивает webhook в Grafana/Alertmanager, и получает 404 без какой-либо подсказки что нужно добавить пустую секцию в конфиг.

## Предложение

Регистрировать маршруты `/alertmanager` и `/grafana` **всегда** (в режиме `serve`), используя дефолтные настройки когда секция в конфиге отсутствует. Это zero-config подход: достаточно иметь хотя бы один чат и API-ключ, чтобы начать принимать алерты.

## Изменения

### 1. `internal/cmd/serve.go` — убрать nil-гейты

**Было:**
```go
if am := cfg.Server.Alertmanager; am != nil {
    amCfg, err := buildAlertmanagerConfig(am, cfg.ConfigPath())
    ...
    srvOpts = append(srvOpts, server.WithAlertmanager(amCfg))
}

if gr := cfg.Server.Grafana; gr != nil {
    grCfg, err := buildGrafanaConfig(gr, cfg.ConfigPath())
    ...
    srvOpts = append(srvOpts, server.WithGrafana(grCfg))
}
```

**Стало:**
```go
am := cfg.Server.Alertmanager
if am == nil {
    am = &config.AlertmanagerYAMLConfig{}
}
amCfg, err := buildAlertmanagerConfig(am, cfg.ConfigPath())
if err != nil {
    return err
}
if amCfg.DefaultChatID == "" && len(cfg.Chats) == 1 {
    for alias := range cfg.Chats {
        amCfg.FallbackChatID = alias
    }
}
srvOpts = append(srvOpts, server.WithAlertmanager(amCfg))

gr := cfg.Server.Grafana
if gr == nil {
    gr = &config.GrafanaYAMLConfig{}
}
grCfg, err := buildGrafanaConfig(gr, cfg.ConfigPath())
if err != nil {
    return err
}
if grCfg.DefaultChatID == "" && len(cfg.Chats) == 1 {
    for alias := range cfg.Chats {
        grCfg.FallbackChatID = alias
    }
}
srvOpts = append(srvOpts, server.WithGrafana(grCfg))
```

Функции `buildAlertmanagerConfig` / `buildGrafanaConfig` уже корректно подставляют дефолтные template и severity/states — изменения внутри них не нужны.

### 2. `internal/server/server.go` — условная регистрация маршрутов сохранена

Nil-гейты `if s.amCfg != nil` / `if s.grCfg != nil` при регистрации маршрутов **сохранены**. Поскольку `serve.go` теперь всегда вызывает `WithAlertmanager`/`WithGrafana`, маршруты всегда регистрируются в режиме `serve`. В режиме `enqueue` (где эти опции не передаются) маршруты не регистрируются — сохраняется чистый 404.

Обновлена логика вывода chatInfo в лог: показывается resolved chat или `"from ?chat_id param"` если чат не задан.

### 3. `internal/server/handler_alertmanager.go` / `handler_grafana.go` — nil-check остаётся

Guard `if s.amCfg == nil` / `if s.grCfg == nil` в хендлерах остаётся как safety net на случай если library-пользователь зарегистрирует маршрут вручную.

### 4. Тесты

- **`TestAlertmanager_NotConfigured`** / **`TestGrafana_NotConfigured`** — без изменений. Сервер без `WithAlertmanager`/`WithGrafana` по-прежнему не регистрирует маршруты (404/405).
- Существующие тесты с `WithAlertmanager(amCfg)` / `WithGrafana(grCfg)` — без изменений.

### 5. Документация (`charts/express-botx/values.yaml`)

Обновить комментарии: объяснить что endpoints включены по умолчанию, секции `alertmanager`/`grafana` нужны только для кастомизации (template, default_chat_id, severity).

## Затрагиваемые файлы

| Файл | Изменения |
|---|---|
| `internal/cmd/serve.go` | Убрать `if am != nil` / `if gr != nil`, подставлять пустой конфиг |
| `internal/server/server.go` | Обновить chatInfo логику (nil-гейты сохранены) |
| `internal/server/server_test.go` | Без изменений (поведение для library-пользователей не изменилось) |
| `charts/express-botx/values.yaml` | Обновить комментарии |

## Шаги реализации

- [x] **Шаг 1.** `internal/cmd/serve.go` — убрать nil-гейты для alertmanager и grafana, подставлять пустой конфиг когда секция отсутствует.
  - **Проверка:** `go build ./...` компилируется без ошибок. Запуск с конфигом без секций `alertmanager`/`grafana` — в логах появляются строки `alertmanager endpoint enabled` и `grafana endpoint enabled`.

- [x] **Шаг 2.** `internal/server/server.go` — обновить chatInfo логику. Nil-гейты при регистрации маршрутов сохранены: `serve.go` всегда передаёт `WithAlertmanager`/`WithGrafana`, поэтому в режиме `serve` маршруты всегда регистрируются. В режиме `enqueue` — чистый 404.
  - **Проверка:** `go vet ./...` без ошибок. Сервер без секций в конфиге отвечает на `POST /api/v1/alertmanager` и `POST /api/v1/grafana` (не 404).

- [x] **Шаг 3.** `internal/server/server_test.go` — тесты `TestAlertmanager_NotConfigured` и `TestGrafana_NotConfigured` без изменений (library-пользователи по-прежнему получают 404/405).
  - **Проверка:** `go test ./internal/server/ -run 'NotConfigured' -v` — оба теста проходят.

- [x] **Шаг 4.** Прогнать полный набор тестов — убедиться что существующие тесты не сломались.
  - **Проверка:** `go test ./...` — все тесты зелёные.

- [x] **Шаг 5.** `charts/express-botx/values.yaml` — обновить комментарии: endpoints включены по умолчанию, секции нужны только для кастомизации.
  - **Проверка:** визуальный осмотр diff.

- [x] **Шаг 6.** Проверка в кластере (skipped - not automatable, requires manual deploy to invitro-dev).
  - **Проверка:** `kubectl-invitro-dev -n sre-api logs <pod>` — в логах `alertmanager endpoint enabled`, `grafana endpoint enabled`. HTTP-ответ ≠ 404.

## Обратная совместимость

Полностью обратно совместимо:
- Пользователи с явной секцией `alertmanager`/`grafana` — поведение не меняется
- Пользователи без секции — получают работающие endpoints с дефолтным шаблоном
- Library-пользователи (`server.New()` без `WithAlertmanager`) — маршрут не регистрируется, 404 как и раньше
