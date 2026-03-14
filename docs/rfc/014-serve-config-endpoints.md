# RFC-014: HTTP-эндпоинты конфигурации (bots, chats)

- **Статус:** Draft
- **Дата:** 2026-03-15

## Контекст

CLI-команды `bot list` и `chats alias list` показывают текущую конфигурацию — какие боты и чаты настроены. Но в Kubernetes-окружении это требует `kubectl exec`, а для внешних систем (мониторинг, дашборды, скрипты деплоя) информация вообще недоступна.

При интеграции с Alertmanager и Grafana часто возникает вопрос: "какие чаты и боты сейчас доступны на этом инстансе?". Ответ можно получить только зайдя в под.

## Предложение

Добавить два read-only GET-эндпоинта в HTTP-сервер, аналогичных CLI-командам:

| Метод | Путь | Аналог CLI | Описание |
|-------|------|------------|----------|
| `GET` | `{basePath}/bot/list` | `bot list` | Список настроенных ботов |
| `GET` | `{basePath}/chats/alias/list` | `chats alias list` | Список чат-алиасов |

### `GET /bot/list`

Возвращает список ботов из конфига. Секреты не раскрываются.

```json
[
  {"name": "deploy-bot", "host": "express.company.ru", "id": "054af49e-..."},
  {"name": "alert-bot", "host": "express.company.ru", "id": "99887766-..."}
]
```

В single-bot mode (env/flags без именованных ботов) возвращает пустой массив `[]` — бот есть, но он не из конфига.

### `GET /chats/alias/list/alias/list`

Возвращает список чат-алиасов с привязками к ботам.

```json
[
  {"name": "deploy", "id": "7ee8aaa9-...", "bot": "deploy-bot"},
  {"name": "alerts", "id": "a1b2c3d4-...", "bot": "alert-bot"},
  {"name": "general", "id": "f40f109f-..."}
]
```

### Аутентификация

Оба эндпоинта требуют аутентификации (API-key или bot-signature), как и `/send`. Конфигурация ботов и чатов — не публичная информация.

### Безопасность

- Секреты ботов (`secret`) **не возвращаются**
- UUID ботов и чатов — не секреты, они используются в API-вызовах и логах

## Изменения

### `internal/server/server.go`

Регистрация новых route в `New()`:

```go
route("GET", "/bot/list", s.handleBotList)
route("GET", "/chats/alias/list", s.handleChatsAliasList)
```

### `internal/server/handler_config.go` (новый файл)

```go
func (s *Server) handleBotList(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(s.botEntries)
}

func (s *Server) handleChatsAliasList(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(s.chatEntries)
}
```

Данные подготавливаются при создании сервера из конфига и хранятся в полях `Server`:

```go
type botEntry struct {
    Name string `json:"name"`
    Host string `json:"host"`
    ID   string `json:"id"`
}

type chatEntry struct {
    Name string `json:"name"`
    ID   string `json:"id"`
    Bot  string `json:"bot,omitempty"`
}
```

### `internal/cmd/serve.go`

Передавать списки ботов и чатов в `server.Config` или через `server.Option`:

```go
srvOpts = append(srvOpts, server.WithConfigInfo(cfg.Bots, cfg.Chats))
```

### `internal/server/api/openapi.yaml`

Новые пути и схемы для `GET /bot/list` и `GET /chats/alias/list`.

### Тесты

- `internal/server/server_test.go` — unit-тесты для handleBots, handleChats
- `internal/cmd/serve_integration_test.go` — интеграционный тест через mock BotX API

## Что НЕ включаем

- **Мутирующие операции** (add, rm, set) — конфиг в Kubernetes read-only (Secret mount), изменения через CLI или Helm values
- **`bot info` / `bot ping`** — требуют обращение к eXpress API, это отдельная задача
- **`chats list`** (список чатов из API, не алиасов) — это уже обращение к BotX API, не конфигурация

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `GET /bot/list` — список ботов | Низкая |
| P0 | `GET /chats/alias/list` — список чат-алиасов | Низкая |
| P0 | OpenAPI spec | Низкая |
| P0 | Тесты | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| CREATE | `internal/server/handler_config.go` — handleBots, handleChats |
| MODIFY | `internal/server/server.go` — новые route, поля для config data |
| MODIFY | `internal/cmd/serve.go` — передача ботов/чатов в server |
| MODIFY | `internal/server/api/openapi.yaml` — новые пути и схемы |
| MODIFY | `internal/server/server_test.go` — тесты |
| MODIFY | `internal/cmd/serve_integration_test.go` — интеграционный тест |

## Проверка

1. `GET /bot/list` — возвращает список ботов без секретов
2. `GET /chats/alias/list` — возвращает алиасы с привязками к ботам
3. Пустой конфиг — `[]` для обоих
4. Без авторизации — 401
5. Формат JSON совпадает с `bot list --format json` и `chats alias list --format json`
