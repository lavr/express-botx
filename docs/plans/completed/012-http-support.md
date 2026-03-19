# RFC-012: Поддержка HTTP и явный порт для eXpress-сервера

- **Статус:** Draft
- **Дата:** 2026-03-14

## Контекст

Сейчас `host` в конфигурации — это просто hostname, к которому express-botx всегда подключается по HTTPS:

```go
// auth.go
url := fmt.Sprintf("https://%s/api/v2/botx/bots/%s/token?signature=%s", host, botID, signature)

// client.go
BaseURL: fmt.Sprintf("https://%s", host)
```

Это создаёт две проблемы:

1. **Интеграционные тесты**: `httptest.NewServer` отдаёт `http://127.0.0.1:<port>` — невозможно подключиться без TLS-хака (`httptest.NewTLSServer` + кастомный `http.Client`) или поддержки HTTP.

2. **Dev/staging окружения**: некоторые внутренние инсталляции eXpress работают по HTTP (за nginx/traefik) или на нестандартном порту. Сейчас это невозможно без внешнего прокси.

## Предложение

Разрешить в поле `host` указывать полный URL: `http://host:port` или `https://host:port`. Если схема не указана — по умолчанию `https://`, обратная совместимость сохраняется.

### Формат

```yaml
bots:
  prod:
    host: express.company.ru              # → https://express.company.ru
  staging:
    host: https://staging.company.ru:8443 # → https://staging.company.ru:8443
  local:
    host: http://localhost:8080            # → http://localhost:8080
```

CLI:

```bash
express-botx send --host http://localhost:8080 --bot-id ... --secret ... "test"
```

Env:

```bash
EXPRESS_HOST=http://localhost:8080
```

### Правила резолва

1. Если `host` начинается с `http://` или `https://` — использовать как есть (удалив trailing `/`)
2. Иначе — `https://` + host
3. Результат — полный base URL: `https://express.company.ru` или `http://localhost:8080`

### Функция

```go
// resolveBaseURL нормализует host в полный base URL.
func resolveBaseURL(host string) string {
    if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
        return strings.TrimRight(host, "/")
    }
    return "https://" + host
}
```

## Изменения

### `internal/botapi/client.go`

```go
func NewClient(host, token string) *Client {
    return &Client{
        BaseURL:    resolveBaseURL(host),
        Token:      token,
        HTTPClient: &http.Client{Timeout: 30 * time.Second},
    }
}
```

### `internal/auth/auth.go`

```go
func GetToken(ctx context.Context, host, botID, signature string) (string, error) {
    baseURL := resolveBaseURL(host)
    url := fmt.Sprintf("%s/api/v2/botx/bots/%s/token?signature=%s", baseURL, botID, signature)
    client := &http.Client{Timeout: 30 * time.Second}
    return doGetToken(ctx, url, client)
}
```

`getTokenWithClient` (тестовый хелпер) больше не нужен — тесты могут передавать `http://127.0.0.1:PORT` как host и использовать стандартный `GetToken`.

### Где разместить `resolveBaseURL`

В `internal/botapi/client.go` — рядом с `NewClient`, единственный потребитель наряду с `auth.GetToken`. Экспортируется как `botapi.ResolveBaseURL` для использования из `auth`.

## Влияние на интеграционные тесты

До этого RFC интеграционные тесты вынуждены использовать `httptest.NewTLSServer` + кастомный `http.Client`, что требует прокидывания клиента через все слои. С поддержкой HTTP:

```go
mockAPI := httptest.NewServer(handler)    // обычный HTTP, без TLS
host := strings.TrimPrefix(mockAPI.URL, "http://")

cfg := fmt.Sprintf(`
bots:
  test:
    host: %s   # http://127.0.0.1:PORT
    id: bot-id
    secret: bot-secret
`, mockAPI.URL)

// runServe() подключится по HTTP — никаких хаков с TLS
```

## Обратная совместимость

Полная. Существующие конфиги с голым hostname (`express.company.ru`) продолжают работать — по умолчанию `https://`.

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | `resolveBaseURL()` + использование в `NewClient` и `GetToken` | Низкая |
| P0 | Удаление `getTokenWithClient` (больше не нужен) | Низкая |
| P0 | Тесты на `resolveBaseURL` | Низкая |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/botapi/client.go` — `resolveBaseURL()`, `NewClient()` |
| MODIFY | `internal/auth/auth.go` — `GetToken()` использует `resolveBaseURL` |
| MODIFY | `internal/auth/auth_test.go` — упрощение: `httptest.NewServer` вместо `NewTLSServer` |

## Проверка

1. `host: express.company.ru` → `https://express.company.ru` (обратная совместимость)
2. `host: https://express.company.ru:8443` → `https://express.company.ru:8443`
3. `host: http://localhost:8080` → `http://localhost:8080`
4. `host: http://localhost:8080/` → `http://localhost:8080` (trailing slash)
5. `--host http://127.0.0.1:9999` — работает из CLI
6. `EXPRESS_HOST=http://localhost:8080` — работает из env
7. Интеграционные тесты с `httptest.NewServer` (HTTP) — работают без TLS-хаков
