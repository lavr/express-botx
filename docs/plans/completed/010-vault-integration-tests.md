# RFC-010: Интеграционные тесты с Vault (OpenBao)

- **Статус:** Implemented
- **Дата:** 2026-03-14

## Контекст

express-botx использует Vault-совместимый API в двух местах:

1. **`secret.Resolve("vault:path#key")`** — чтение секретов (bot secret, API keys) из KV v2
2. **`token.VaultCache`** — кэширование BotX-токенов в KV v2

Сейчас оба компонента тестируются через `httptest.Server` — мок имитирует HTTP API. Это покрывает формат запросов/ответов, но не гарантирует совместимость с настоящим сервером (формат путей, KV v2 versioning, обработка ошибок).

## Решение

Интеграционные тесты запускают [OpenBao](https://openbao.org/) (open-source fork Vault, API-совместимый) в dev-режиме как процесс. Также поддерживается оригинальный `vault` binary — хелпер ищет сначала `bao`, затем `vault`.

### Почему binary, а не Docker

- Старт ~0.5 сек (vs ~3 сек для контейнера)
- Не требует Docker / Docker-in-Docker в CI
- Локально: `brew install openbao`

### Что тестируем

#### `token.VaultCache` (кэш токенов)

| Тест | Описание |
|------|----------|
| `TestVaultIntegration_SetGet` | `Set` + `Get` — запись и чтение токена, miss на пустом кэше |
| `TestVaultIntegration_Expiry` | `Set` с коротким TTL → `Get` после истечения → miss |
| `TestVaultIntegration_MultipleKeys` | Три ключа в одном KV-секрете |
| `TestVaultIntegration_Overwrite` | Повторный `Set` для того же ключа перезаписывает значение |
| `TestVaultIntegration_WrongToken` | Запрос с неверным токеном → ошибка |

#### `secret.Resolve` (KV v2)

| Тест | Описание |
|------|----------|
| `TestVaultResolve_ReadSecret` | Записать секрет → прочитать через `Resolve("vault:path#key")` |
| `TestVaultResolve_MissingKey` | Чтение несуществующего ключа → ошибка |
| `TestVaultResolve_MissingPath` | Чтение по несуществующему пути → ошибка |

### Build tag

Тесты отделяются от unit-тестов build-тегом `//go:build vault`:

```bash
# Интеграционные тесты
go test -tags vault ./...

# Только unit-тесты (по умолчанию)
go test ./...
```

Если binary не найден — тесты пропускаются через `t.Skip`.

### Изоляция

Каждый тест использует уникальный путь `secret/data/test-<random>`.

### Хелперы

```go
// internal/testutil/vault.go

// StartVault запускает bao/vault server -dev и возвращает адрес и root token.
// Останавливается автоматически через t.Cleanup.
func StartVault(t *testing.T) (addr, token string)

// WriteSecret записывает данные в KV v2 через HTTP API.
func WriteSecret(t *testing.T, addr, token, path string, data map[string]string)
```

`StartVault` логика:

1. `exec.LookPath("bao")`, fallback на `exec.LookPath("vault")`
2. Если не найден — `t.Skip`
3. Свободный порт через `net.Listen("tcp", ":0")`
4. `bao server -dev -dev-listen-address=... -dev-root-token-id=test-root-token`
5. Poll `GET /v1/sys/health` (timeout 10 сек)
6. `t.Cleanup` → `cmd.Process.Kill()`

## CI: GitHub Actions

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - run: go test ./...

  test-vault:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - name: Install OpenBao
        run: |
          curl -fsSL https://github.com/openbao/openbao/releases/latest/download/bao_$(curl -s https://api.github.com/repos/openbao/openbao/releases/latest | jq -r .tag_name | tr -d v)_linux_amd64.deb -o /tmp/openbao.deb
          sudo dpkg -i /tmp/openbao.deb
      - run: go test -tags vault ./...
```

### Локальный запуск

```bash
# macOS
brew install openbao
go test -tags vault ./...

# Linux
# скачать openbao: https://github.com/openbao/openbao/releases
go test -tags vault ./...
```

## Структура файлов

| Файл | Описание |
|------|----------|
| `internal/testutil/vault.go` | `StartVault()`, `WriteSecret()` хелперы |
| `internal/token/vault_integration_test.go` | Тесты VaultCache с реальным сервером |
| `internal/secret/vault_integration_test.go` | Тесты Resolve("vault:...") с реальным сервером |

Все файлы с `//go:build vault`.

## Что НЕ включаем

- ACL-политики и не-root токены — тестируем с root token
- HA / кластер — dev-режим достаточен
- Тесты auto-unseal / agent — не относится к express-botx
