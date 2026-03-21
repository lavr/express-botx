# План реализации команды `api`

Команда `express-botx api` — отправка HTTP-запросов к eXpress BotX API с автоматической аутентификацией. Поддерживает JSON-запросы через `-f`/`-F`, raw-тело через `--input`, и multipart-загрузку через `--input @file` + `-f`. Дизайн вдохновлён `gh api`.

## Синтаксис

```
express-botx api [flags] <endpoint>
```

**Endpoint** — путь к API, например `/api/v3/botx/chats/list`. Слеш в начале обязателен — в отличие от `gh api`, здесь нет shorthand-нотации (все эндпоинты начинаются с `/api/`).

## Примеры использования

```bash
# GET-запрос (метод по умолчанию)
express-botx api /api/v3/botx/chats/list

# GET с query-параметрами (кавычки обязательны из-за ? в zsh)
express-botx api '/api/v3/botx/chats/info?group_chat_id=<UUID>'

# POST с JSON-телом из аргументов
express-botx api -X POST /api/v3/botx/chats/create -f name=test -f chat_type=group_chat

# POST с JSON-телом из файла (raw mode — Content-Type нужно указать явно)
express-botx api -X POST /api/v4/botx/notifications/direct \
  --input payload.json -H 'Content-Type: application/json'

# POST raw body с кастомным Content-Type
express-botx api -X POST /api/v3/botx/smartapps/event \
  --input event.xml -H 'Content-Type: application/xml'

# Скачать файл (бинарный ответ → stdout, перенаправить в файл)
express-botx api '/api/v3/botx/files/download?group_chat_id=<UUID>&file_id=<UUID>' > photo.jpg

# Загрузить файл (multipart/form-data)
express-botx api -X POST /api/v3/botx/files/upload \
  --input @photo.jpg \
  -f group_chat_id=<UUID> -f file_name=photo.jpg -f mime_type=image/jpeg

# Фильтрация ответа через jq-выражение
express-botx api /api/v3/botx/chats/list -q '.result[].name'

# Показать заголовки ответа
express-botx api -i /api/v3/botx/chats/list

# Кастомный заголовок
express-botx api -H "X-Custom: value" /api/v3/botx/chats/list
```

## Флаги

| Флаг | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `-X, --method` | string | — (авто) | HTTP-метод. Если не указан: `POST` при наличии `-f`/`-F`/`--input`, иначе `GET` |
| `-f, --field` | key=value (повтор.) | — | Строковое поле для JSON-тела запроса. Значение всегда строка |
| `-F` | key=value (повтор.) | — | Типизированное поле: `true`/`false` → bool, целые числа → number, `@file` → содержимое файла |
| `-H, --header` | key:value (повтор.) | — | Дополнительный HTTP-заголовок |
| `--input` | string | — | Файл с телом запроса (или `-` для stdin). С префиксом `@` — бинарный файл для multipart (см. ниже). Без `@` — текстовое тело as-is |
| `--part-name` | string | `content` | Имя multipart-part для бинарного файла из `--input @file`. Игнорируется вне multipart-режима |
| `-q, --jq` | string | — | jq-выражение для фильтрации JSON-ответа |
| `-i, --include` | bool | `false` | Показать HTTP-статус и заголовки ответа |
| `--timeout` | duration | — | Таймаут HTTP-запроса. Если не задан, используется значение из конфига бота (`bot_timeout`, по умолчанию 10s). Флаг `--timeout` перезаписывает конфиг |
| `--silent` | bool | `false` | Подавить вывод тела ответа (полезно с `-i`) |
| + все глобальные флаги | | | `--config`, `--bot`, `--host`, `--bot-id`, `--secret`, `--token`, `--no-cache`, `--format`, `--verbose` |

### Взаимоисключения и приоритет флагов

Источники тела запроса **взаимоисключающие**. При нарушении — немедленная ошибка до отправки запроса:

| Комбинация | Результат |
|------------|-----------|
| Только `-f`/`-F` | JSON body из полей |
| Только `--input` (без `@`) | Raw body as-is |
| `--input @file` (с или без `-f`) | Multipart: `@file` → бинарный part, `-f` → текстовые parts (если есть) |
| `--input @file` + `-F` | **Ошибка**: `-F is not supported in multipart mode, use -f for text parts` |
| `--input` (без `@`) + `-f`/`-F` | **Ошибка**: `--input and -f/-F are mutually exclusive (use --input @file for multipart)` |
| `-f` + `-F` в одном запросе | OK — объединяются в один JSON-объект |

## Режимы формирования тела запроса

### 1. JSON mode (`-f`/`-F` без `--input @file`)

Поля собираются в JSON-объект, `Content-Type: application/json`.

### 2. Raw mode (`--input` без `@`)

Тело отправляется as-is. Content-Type **не выставляется автоматически** — пользователь задаёт его через `-H`. Это позволяет отправлять XML, plain text, произвольный JSON из файла и т.д.

### 3. Multipart mode (`--input @file`, опционально + `-f`)

Для эндпоинтов типа `/api/v3/botx/files/upload`, которые требуют `multipart/form-data`:
- `--input @photo.jpg` → part с именем `content` и бинарным содержимым файла (всегда присутствует)
- `-f group_chat_id=UUID` → текстовые parts (опционально, 0 или более)
- `Content-Type: multipart/form-data; boundary=...` выставляется автоматически
- `--part-name file` переопределяет имя part для бинарного содержимого (по умолчанию `content`)

## Доступные эндпоинты

Справочник эндпоинтов см. в `docs/express-api/`.

## Архитектура

### Новые файлы

```
internal/cmd/api.go          — реализация команды (runApi)
internal/cmd/api_test.go     — тесты
```

### Изменения в существующих файлах

```
internal/cmd/cmd.go          — добавить case "api" в диспетчер Run()
```

### Без изменений в botapi/client.go

Команда `api` НЕ использует `botapi.Client`. Она напрямую выполняет HTTP-запрос через `net/http`, так как суть команды — отправить произвольный запрос к произвольному эндпоинту. Переиспользуются только:
- `botapi.ResolveBaseURL(host)` — построение base URL
- `authenticate()` из `cmd.go` — получение токена

## Пошаговый план реализации

### Шаг 1. Регистрация команды

В `internal/cmd/cmd.go`:
- Добавить `case "api"` → `return runApi(args[1:], deps)` в `Run()`
- Добавить `api` в список субкоманд в сообщении об ошибке и в `printUsage`

### Шаг 2. Парсинг флагов (`internal/cmd/api.go`)

Создать `runApi(args []string, deps Deps) error`:

1. Создать `flag.FlagSet("api", ...)`
2. Зарегистрировать глобальные флаги через `globalFlags(fs, &flags)`
3. Зарегистрировать специфичные флаги:
   - `-X`/`--method` → `string` (по умолчанию `""` — пустая строка как sentinel для автовыбора)
   - `-f`/`--field` → custom `stringSlice` (повторяемый флаг, формат `key=value`)
   - `-F` → custom `stringSlice` (повторяемый флаг, формат `key=value`, без long-alias)
   - `-H`/`--header` → custom `stringSlice` (повторяемый флаг, формат `key:value`)
   - `--input` → `string`
   - `--part-name` → `string` (по умолчанию `content`)
   - `-q`/`--jq` → `string`
   - `-i`/`--include` → `bool`
   - `--timeout` → `duration` (по умолчанию `0`, означает "использовать конфиг")
   - `--silent` → `bool`
4. `reorderArgs(fs, args)` + `fs.Parse(...)`
5. **Позиционный аргумент** — endpoint:
   - Ровно один позиционный аргумент обязателен; если `len(fs.Args()) == 0` → ошибка `"endpoint required"`; если `> 1` → ошибка `"unexpected arguments"`
   - Должен начинаться с `/` → иначе ошибка `"endpoint must start with /"`
   - Полные URL (`http://...`, `https://...`) запрещены → ошибка `"endpoint must be a path, not a full URL"`
6. **Валидация взаимоисключений**:
   - `--input` (без `@`) + `-f`/`-F` → ошибка `"--input and -f/-F are mutually exclusive"`
   - `--input @file` + `-F` → ошибка `"-F is not supported in multipart mode, use -f for text parts"` (в multipart `-F` с автоприведением типов не имеет смысла — все part-значения строковые)
7. **Ранняя валидация `-q`**: если задан, вызвать `gojq.Parse(expr)`. При ошибке — немедленная ошибка `"invalid jq expression: ..."`, запрос не отправляется

**Повторяемые флаги**: реализовать через `type stringSlice []string` с методами `String()` и `Set()` (стандартный паттерн для `flag`). Go-пакет `flag` не поддерживает short aliases напрямую — зарегистрировать оба имени на одну переменную: `fs.Var(&fields, "f", "...")` и `fs.Var(&fields, "field", "...")`.

**Важно**: написать тест на `reorderArgs` с повторяемыми флагами (например, `-f a=1 -f b=2 /api/endpoint`), чтобы убедиться, что позиционный аргумент (endpoint) не теряется среди них.

### Шаг 2.5. Загрузка и валидация конфигурации

Сразу после `fs.Parse` и до построения запроса — загрузить конфигурацию по тому же паттерну, что и в `runSend`/`runChatsList`:

```go
// Взаимоисключение секретов
if flags.Secret != "" && flags.Token != "" {
    return fmt.Errorf("--secret and --token are mutually exclusive")
}

// Загрузка конфига (multi-bot, env, YAML, overrides из флагов)
cfg, err := config.Load(flags)
if err != nil {
    return err
}

// Валидация --format (json/text/...)
if err := cfg.ValidateFormat(); err != nil {
    return err
}
```

**Аутентификация НЕ вызывается здесь** — она откладывается до шага 3.5, после того как все входные данные (endpoint, `-f`, `-F`, `--input`, файлы) провалидированы и тело запроса построено. Это соответствует паттерну `runSend`, где `authenticate()` вызывается только после полной валидации параметров и построения `SendRequest`.

Этот шаг обеспечивает:
- Корректную работу `--config`, `--bot`, `--host`, `--bot-id`, `--secret`, `--token` и env-переменных
- Проверку `--secret` vs `--token`
- Валидацию `--format`
- Получение `cfg.Host` и `cfg.HTTPTimeout()` для шагов 3–4

### Шаг 3. Построение тела запроса

Шаг 3 разделён на две фазы: **построение тела** (до аутентификации) и **сборка http.Request** (после, в шаге 4). Это позволяет сначала провалидировать все локальные входы, затем аутентифицироваться, и только потом создать финальный запрос.

```go
// apiBody содержит результат построения тела запроса.
// Все данные уже прочитаны в память ([]byte) — body всегда replayable для retry на 401.
type apiBody struct {
    data        []byte // сериализованное тело (JSON, raw, multipart) или nil для GET
    contentType string // Content-Type или "" (raw mode — пользователь задаёт через -H)
    method      string // финальный HTTP-метод (после автовыбора)
}

func buildAPIBody(p apiBodyParams) (*apiBody, error)
```

```go
type apiBodyParams struct {
    method      string     // HTTP-метод (или пустая строка для автовыбора)
    fields      []string   // -f key=value
    typedFields []string   // -F key=value
    inputFile   string     // --input (путь к файлу, "-" для stdin, "@file" для multipart)
    partName    string     // --part-name (по умолчанию "content")
    stdin       io.Reader  // deps.Stdin
}
```

1. **Валидация**: проверить взаимоисключения `--input` vs `-f`/`-F` (если не multipart mode)

2. **Определение метода**: если `method` пуст:
   - Если есть `-f`, `-F` или `--input` → `POST`
   - Иначе → `GET`

3. **Определение режима и построение тела**:
   - **Multipart mode** (`inputFile` начинается с `@`):
     - Создать `multipart.Writer` поверх `bytes.Buffer`
     - Добавить бинарный part с именем `partName` (по умолчанию `content`) из файла
     - Добавить текстовые parts из `-f` полей
     - Content-Type = writer.FormDataContentType()
   - **Raw mode** (`inputFile` задан, не `@`):
     - `-` → `io.ReadAll(stdin)`
     - иначе → `os.ReadFile(path)`
     - **Content-Type не выставляется** (пользователь задаёт через `-H`)
   - **JSON mode** (есть `-f`/`-F`, нет `--input @file`):
     - Собрать JSON-объект из полей
     - `-f key=value` → `"key": "value"` (всегда строка)
     - `-F key=value` → автоприведение типов:
       - `true`/`false` → `bool`
       - целое число → `number`
       - `@filename` → содержимое файла как строка
     - Сериализовать в JSON, `Content-Type: application/json`
   - **No body**: метод `GET`/`DELETE` без полей и input — data = nil

**Важно**: все источники (stdin, файлы) читаются **целиком в `[]byte`** на этом шаге. Это:
- Позволяет обнаружить ошибки чтения (несуществующий файл, обрыв stdin) до аутентификации
- Гарантирует, что тело всегда replayable для retry на 401 (stdin уже вычитан и сохранён в `data`)

**Лимит размера тела запроса**: буферизация ограничена **50 МБ** (`maxRequestBodySize`). При превышении — ошибка `"request body too large (max 50MB)"`. Это осознанный компромисс: eXpress BotX API не рассчитан на загрузку гигабайтных файлов, а 50 МБ покрывает все практические сценарии (фото, документы, логи). Лимит задаётся константой и может быть увеличен при необходимости.

### Шаг 3.5. Аутентификация

**Пропускается**, если пользователь передал `-H "Authorization: ..."` (manual auth mode).

Иначе — после того как все входные данные провалидированы и тело построено (шаги 2–3), вызвать аутентификацию:

```go
tok, cache, err := authenticate(cfg)
if err != nil {
    return err
}
```

Это гарантирует, что пользователь сначала получит понятные локальные ошибки (невалидный endpoint, плохой формат `-f`, несуществующий файл в `--input`, взаимоисключающие флаги), а не сетевую/auth ошибку.

### Шаг 4. Сборка http.Request, выполнение и вывод ответа

#### Сборка http.Request

Из `apiBody` (шаг 3), токена (шаг 3.5) и конфига собирается финальный `http.Request`:

```go
func buildHTTPRequest(ctx context.Context, body *apiBody, endpoint, host, token string, headers []string) (*http.Request, error)
```

1. **URL**: `ResolveBaseURL(host) + endpoint`
2. **Body**: `bytes.NewReader(body.data)` (или nil). Поскольку `body.data` — это `[]byte`, `bytes.NewReader` можно создать повторно для retry.
3. **Заголовки** (порядок применения):
   - `Authorization: Bearer <token>` — **только если** `-H "Authorization: ..."` не задан пользователем (см. ниже)
   - Content-Type из `body.contentType` (если не пуст)
   - Кастомные из `-H`: парсить по первому `:` → `key: value`. Перезаписывают дефолтные заголовки (например, `-H "Content-Type: text/plain"` заменит дефолтный)
4. `http.NewRequestWithContext(ctx, body.method, url, reader)`

**Override Authorization**: если пользователь передал `-H "Authorization: ..."`, команда работает в режиме **manual auth**:
- Аутентификация (шаг 3.5) пропускается — `authenticate()` не вызывается, `--secret`/`--token` не требуются
- Retry на 401 отключается — пользователь сам управляет токеном
- Переданный заголовок используется as-is

Это позволяет использовать `api` с внешними токенами или для отладки auth-проблем, аналогично `gh api -H "Authorization: token ..."`. Проверка наличия `Authorization` в `-H` выполняется case-insensitive по имени заголовка.

#### Таймаут

Приоритет таймаута для основного API-запроса (от высшего к низшему):
1. `--timeout` флаг (если задан явно)
2. `cfg.HTTPTimeout()` (из YAML конфига / env, по умолчанию 10s)

Таймаут применяется через `http.Client{Timeout: ...}`.

**Scope**: `--timeout` управляет только основным HTTP-запросом к API. Аутентификация (`authenticate()`, `refreshToken()`) использует `cfg.HTTPTimeout()` напрямую (см. `cmd.go:174`, `cmd.go:199`) — это существующее поведение всех команд. Менять plumbing аутентификации ради одного флага непропорционально; при необходимости пользователь может задать `bot_timeout` в конфиге, что повлияет на обе фазы.

#### Выполнение, retry на 401, стриминг ответа

Ключевое решение: **ответ обрабатывается по-разному в зависимости от статуса**. Это позволяет стримить большие 2xx-ответы (скачивание файлов) без буферизации, при этом корректно обрабатывать 401.

Порядок выполнения:

1. Отправить запрос, получить `resp`
2. **Проверить статус до чтения тела**:
   - **HTTP 401**:
     - **Manual auth** (пользователь передал `-H "Authorization: ..."`): retry не делается, 401 обрабатывается как обычный non-2xx (тело в stdout, exit 1)
     - **Auto auth** — перехватить до любого вывода:
       - Закрыть `resp.Body` (тело 401-ответа не нужно)
       - **Статический токен** (`cfg.BotToken != ""`): вернуть Go error `"bot token rejected (401), re-configure token"`
       - **Secret-based**: `refreshToken(cfg, cache)`, пересобрать `http.Request` с новым токеном, повторить запрос. Если повтор тоже 401 — вернуть Go error
   - **HTTP 2xx** — перейти к стримингу (п. 3)
   - **Другой non-2xx** (4xx/5xx, кроме 401) — прочитать тело целиком в `[]byte`, затем вывести (п. 3). Тела ошибок API обычно маленькие (JSON с описанием ошибки), буферизация безопасна
3. Вывод ответа (см. «Порядок вывода»)

Для retry на 401 тело **запроса** всегда replayable (`body.data` — `[]byte`). Тело **ответа** 401 отбрасывается, пользователь видит только финальный ответ.

#### Контракт stdout/stderr

Контракт определяет **три категории ситуаций** с чётким поведением:

| Категория | stdout | stderr | Exit code | Тип ошибки |
|-----------|--------|--------|-----------|------------|
| **Ошибка до запроса** (валидация, IO, auth) | ничего | `error: ...` | 1 | Go `error` |
| **HTTP 2xx** | тело ответа | пусто* | 0 | nil |
| **HTTP non-2xx** (кроме 401-retry) | тело ответа | пусто* | 1 | `*ExitError` |

\* stderr может содержать verbose-лог (`-v`) и предупреждения `-q`/`--format json` (см. ниже).

**401 — особый случай**: это не «non-2xx для пользователя», а внутренний auth-механизм. При статическом токене 401 возвращается как Go error (категория 1). При secret-based — retry прозрачен; если retry успешен — пользователь видит финальный ответ; если retry тоже 401 — Go error.

```go
// ExitError — общий тип для команд, которым нужен ненулевой exit code
// без печати "error: ..." в stderr. Определяется в пакете cmd, используется
// в main.go через errors.As — не привязан к конкретной субкоманде.
type ExitError struct {
    Code int
}

func (e *ExitError) Error() string {
    return fmt.Sprintf("exit status %d", e.Code)
}
```

#### Порядок вывода

1. Если `-i`: напечатать в stdout `{resp.Proto} {status_code} {status_text}` + заголовки + пустая строка (используется реальный `resp.Proto`, например `HTTP/1.1` или `HTTP/2.0`)
2. Если `--silent`: пропустить тело
3. Иначе обработать тело:

**Для HTTP 2xx (стриминг)**:
   - Если `-q`: прочитать тело целиком, обработать через gojq (стриминг невозможен — jq нужен полный JSON). При невалидном JSON — вывести raw body в stdout, предупреждение в stderr. Невалидное jq-выражение уже отсечено на этапе парсинга флагов (шаг 2)
   - Если `--format json` и Content-Type содержит `application/json`: прочитать целиком, pretty-print. При невалидном JSON — raw body в stdout
   - Иначе: **стримить** `resp.Body` → stdout через `io.Copy`. Это O(1) по памяти и поддерживает скачивание больших файлов

**Для HTTP non-2xx (буфер)**:
   - Тело уже прочитано в `[]byte` на шаге 2
   - `-q` / `--format json` / raw — та же логика, что для 2xx, но работает с буфером

4. **Exit code**:
   - `0` — HTTP 2xx (включая успешный retry после 401)
   - `1` — HTTP non-2xx (тело ответа в stdout)

### Шаг 5. Обработка ошибок

- **401 retry** — описан в шаге 4 («Выполнение и retry на 401»). Паттерн из `runSend` (см. `send.go:177`): статический токен → немедленная ошибка; secret-based → `refreshToken(cfg, cache)` перезаписывает кеш через `cache.Set()`, повтор запроса с новым токеном.
- При ошибке сети — вернуть понятную ошибку (попадёт в stderr через main.go)

### Шаг 6. Реализация jq-фильтрации

Встроенная jq-фильтрация через Go-библиотеку [`itchyny/gojq`](https://github.com/itchyny/gojq):
- `gojq.Parse(expr)` → `code.Run(input)` — полностью in-process, без внешних зависимостей
- Поддерживает основной синтаксис jq (`.field`, `.[0]`, `| select(...)`, и т.д.)
- Никаких runtime-зависимостей: `-q` работает из коробки в любом режиме установки (binary, Homebrew, Docker)

**Ранняя валидация**: `gojq.Parse(expr)` вызывается в шаге 2 (парсинг флагов), до аутентификации и запроса. Если выражение невалидно — немедленная ошибка (`"invalid jq expression: ..."`), Go error, запрос не отправляется. Это дешёвая проверка без побочных эффектов.

Поведение при ошибках выполнения (после получения ответа):
- **Валидное выражение, но тело не JSON**: raw body в stdout as-is, предупреждение в stderr
- **Валидное выражение, валидный JSON**: результат фильтрации в stdout

### Шаг 7. Тесты

Написать тесты в `internal/cmd/api_test.go`:

1. **Парсинг флагов**:
   - Корректный разбор `-X`, `-f`, `-F`, `-H`, `--input`, `-q`, `-i`
   - Автовыбор метода (GET по умолчанию, POST при наличии полей)
   - Ошибка при отсутствии endpoint
   - **Ошибка при `--input` + `-f` (без `@`)** — валидация взаимоисключений

2. **Построение тела запроса по режимам**:
   - **JSON mode**: `-f key=value` → строковое значение; `-F key=123` → числовое; `-F key=true` → булево; `-F key=@file` → содержимое файла
   - **Raw mode**: `--input file.json` → as-is body, без Content-Type
   - **Multipart mode**: `--input @photo.jpg -f group_chat_id=UUID` → multipart/form-data с бинарным part

3. **HTTP-запрос** (httptest.NewServer):
   - Отправка GET без тела
   - Отправка POST с JSON-телом
   - Кастомные заголовки
   - Передача Authorization Bearer
   - **Бинарный ответ** — raw body без искажений
   - **Multipart upload** — сервер получает корректные parts

4. **Вывод и контракт stdout/stderr**:
   - Формат ответа (raw, pretty JSON)
   - Флаг `-i` (заголовки в stdout)
   - Флаг `--silent`
   - jq-фильтрация
   - **Non-2xx**: тело в stdout, `runApi` возвращает `*ExitError{Code: 1}`, stderr чистый (exit code проверяется на уровне e2e, не unit-теста)
   - **Non-JSON ответ + `-q`**: raw body в stdout as-is, предупреждение в stderr
   - **Невалидное `-q` выражение**: ошибка на этапе парсинга, запрос не отправляется
   - **Большой 2xx-ответ**: стримится через `io.Copy`, не буферизуется целиком

5. **Таймаут**:
   - `--timeout 1s` перезаписывает конфиг
   - Без `--timeout` — используется `cfg.HTTPTimeout()`

6. **Обработка ошибок**:
   - 401 → retry (в т.ч. с `--input -` — тело уже в `[]byte`, retry безопасен)
   - Невалидный endpoint
   - Невалидный формат `-f` (нет `=` в значении)
   - Взаимоисключающие флаги

### Шаг 8. Обновить help / usage

В `printUsage()` в `cmd.go` добавить строку:
```
  api        Make authenticated API requests to eXpress
```

### Шаг 9. Обновить main.go

В `main.go` добавить обработку `*ExitError` — это общий механизм для любой команды, которой нужен ненулевой exit code без печати ошибки в stderr:

```go
if err := cmd.Run(os.Args[1:], deps); err != nil {
    var exitErr *cmd.ExitError
    if errors.As(err, &exitErr) {
        os.Exit(exitErr.Code)
    }
    fmt.Fprintf(os.Stderr, "error: %v\n", err)
    os.Exit(1)
}
```

`ExitError` определён в пакете `cmd` и не привязан к команде `api` — любая будущая команда может использовать его для управления exit code.

### Шаг 10. Документация

#### README.md

В секцию «Возможности» добавить пункт:

```
- **API-запросы** — произвольные вызовы eXpress BotX API из командной строки с автоаутентификацией
```

В секцию «Quick Start» после блока «Создание конфига» добавить:

```markdown
### API-запросы

Отправить произвольный запрос к BotX API:

```bash
# Список чатов
express-botx api /api/v3/botx/chats/list

# POST с JSON-телом
express-botx api -X POST /api/v3/botx/chats/create -f name=test -f chat_type=group_chat

# Фильтрация ответа
express-botx api /api/v3/botx/chats/list -q '.result[].name'
```

Подробнее: [docs/commands.md](docs/commands.md#api)
```

#### docs/commands.md

В таблицу «Обзор» добавить строку (после `send`):

```
| `api` | Отправить произвольный HTTP-запрос к BotX API |
```

После секции `send` добавить полную секцию `api`:

~~~markdown
---

## api

Отправляет произвольный HTTP-запрос к eXpress BotX API с автоматической аутентификацией. Поддерживает JSON-тело через `-f`/`-F`, raw body через `--input`, multipart-загрузку через `--input @file`, фильтрацию ответа через jq-выражения (`-q`).

### Примеры

```bash
# GET-запрос
express-botx api /api/v3/botx/chats/list

# GET с query-параметрами
express-botx api '/api/v3/botx/chats/info?group_chat_id=<UUID>'

# POST с JSON-телом из полей
express-botx api -X POST /api/v3/botx/chats/create -f name=test -f chat_type=group_chat

# POST с JSON-телом из файла (raw mode)
express-botx api -X POST /api/v4/botx/notifications/direct \
  --input payload.json -H 'Content-Type: application/json'

# POST raw body с кастомным Content-Type
express-botx api -X POST /api/v3/botx/smartapps/event \
  --input event.xml -H 'Content-Type: application/xml'

# Загрузить файл (multipart)
express-botx api -X POST /api/v3/botx/files/upload \
  --input @photo.jpg \
  -f group_chat_id=<UUID> -f file_name=photo.jpg -f mime_type=image/jpeg

# Скачать файл
express-botx api '/api/v3/botx/files/download?group_chat_id=<UUID>&file_id=<UUID>' > photo.jpg

# Фильтрация через jq
express-botx api /api/v3/botx/chats/list -q '.result[].name'

# Показать заголовки ответа
express-botx api -i /api/v3/botx/chats/list
```

При HTTP 2xx — exit 0. При non-2xx — тело ответа выводится в stdout, exit 1. Ошибки валидации и auth выводятся в stderr (exit 1, stdout пустой).

### Флаги

```
-X, --method     HTTP-метод (авто: POST при -f/-F/--input, иначе GET)
-f, --field      строковое поле для JSON-тела (key=value, повторяемый)
-F               типизированное поле: true/false → bool, числа → number, @file → содержимое
-H, --header     дополнительный HTTP-заголовок (key:value, повторяемый)
--input          файл с телом запроса (- для stdin, @file для multipart)
--part-name      имя multipart-part для бинарного файла (по умолчанию: content)
-q, --jq         jq-выражение для фильтрации JSON-ответа
-i, --include    показать HTTP-статус и заголовки ответа
--timeout        таймаут запроса (перезаписывает значение из конфига)
--silent         подавить вывод тела ответа
```

### Режимы тела запроса

| Режим | Флаги | Content-Type |
|-------|-------|-------------|
| JSON | `-f`/`-F` | `application/json` (авто) |
| Raw | `--input file` | не выставляется — задать через `-H` |
| Multipart | `--input @file` [+ `-f`] | `multipart/form-data` (авто) |

`-f`/`-F` и `--input` (без `@`) взаимоисключающие. `-F` запрещён в multipart-режиме.
~~~

## Порядок реализации

Тесты пишутся на каждом этапе, а не отдельным шагом.

- [x] **Минимальный MVP** (шаги 1–5, включая 2.5 и 3.5, и 9): JSON mode + raw mode, `--method`, `--input`, `-f`, `-i`, `--timeout`, загрузка конфига, аутентификация, контракт stdout/stderr + тесты.
- [x] **jq-фильтрация** (шаг 6): добавить `-q` через встроенный `gojq` + тесты.
- [x] **Типизированные поля** (расширение шага 3): добавить `-F` с автоприведением типов + тесты.
- [x] **Multipart mode** (расширение шага 3): `--input @file` + `-f` для файловых эндпоинтов + тесты.
- [x] **Документация** (шаг 10): обновить README.md и docs/commands.md.

## Отличия от `gh api`

| Возможность | `gh api` | `express-botx api` |
|-------------|----------|---------------------|
| Плейсхолдеры `{owner}` | Да | Нет (не нужны) |
| GraphQL | Да | Нет (eXpress — только REST) |
| `--paginate` | Да | Нет (eXpress API не использует cursor-пагинацию) |
| `--cache` | Да | Нет (не актуально для CLI-утилиты) |
| `--template` (Go templates) | Да | Нет (jq достаточно) |
| Авто-метод | GET → POST при полях | Аналогично |
| `-f` / `-F` | Да | Да |
| `--input` | Да | Да (+ `@file` для multipart) |
| `-q` (jq) | Да | Да (встроенный gojq, без внешних зависимостей) |
| `-i` (include headers) | Да | Да |
| `--silent` | Да | Да |
| Retry при 401 | Нет | Да (специфика токенов eXpress) |
| Multipart upload | Нет | Да (через `--input @file`) |
| Binary download | Неявно | Да (raw body в stdout) |
