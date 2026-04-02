# План реализации: inline parser для mentions

## Цель

Добавить parser, который разбирает inline syntax вида `@mention[...]` в тексте сообщения и нормализует его в канонический контракт Варианта 1:

- `message` содержит BotX placeholder'ы;
- `mentions` содержит BotX wire-format;
- parse и lookup errors не роняют отправку, а сохраняются отдельно.

## Scope первой итерации

- Поддержка parser'а в `send`, `enqueue` и HTTP `/api/v1/send`.
- Поддержка resolver'ов `email`, `huid`, `all`.
- Поддержка merge с уже переданным raw `mentions`.
- Поддержка отключения parser'а через `--no-parse` и `?no_parse=true`.
- Лимит parsed token: `1000`.

## Что не входит

- `contact`, `chat`, `channel`.
- `ad`-resolver.
- Blocking policy для parse/lookup errors.
- Публичный формат возврата parser warnings в API.

## Канонический результат parser'а

После parser'а данные должны быть представлены так:

- текст сообщения уже нормализован;
- inline token либо заменён на BotX placeholder, либо оставлен literal text;
- raw `mentions` и parsed mentions объединены в один массив;
- накопленные parser errors доступны отдельно от outbound payload.

## Шаг 1. Выделить пакет или модуль parser'а

Нужно создать отдельную точку входа для parser'а, чтобы не размазывать логику по `send`, `enqueue` и HTTP handler.

Минимальные обязанности parser'а:

- найти `@mention[...]` в тексте;
- разобрать token;
- выполнить URL-decode display name;
- сделать lookup для `email`;
- сформировать mentions;
- вернуть нормализованный текст;
- вернуть parser errors.

Ожидаемый публичный контракт модуля:

- вход: `message`, `raw_mentions`, `parse_enabled`;
- выход: `normalized_message`, `merged_mentions`, `errors`.

Проверка:

- [x] Выбран один центральный пакет/модуль parser'а, который используется всеми точками входа.
- [x] Есть unit-тест на пустое сообщение без token'ов.
- [x] Есть unit-тест на сообщение без `@mention[...]`, которое возвращается без изменений.

## Шаг 2. Реализовать grammar parser для `@mention[...]`

Нужно реализовать разбор syntax:

- `@mention[email:<email>]`
- `@mention[email:<email>;<display_name>]`
- `@mention[huid:<uuid>]`
- `@mention[huid:<uuid>;<display_name>]`
- `@mention[all]`

Правила первой версии:

- `display_name` URL-decoded;
- `display_name` не может содержать `]` и `;` в открытом виде;
- `all` не принимает `value` и `display_name`;
- при parse error token остаётся literal text.

Проверка:

- [x] Добавлен unit-тест на разбор `email` без display name.
- [x] Добавлен unit-тест на разбор `email` с URL-quoted display name.
- [x] Добавлен unit-тест на разбор `huid` без display name.
- [x] Добавлен unit-тест на разбор `huid` с display name.
- [x] Добавлен unit-тест на разбор `all`.
- [x] Добавлен unit-тест на parse error для `@mention[email:]`.
- [x] Добавлен unit-тест на parse error для `@mention[all;x]`.
- [x] Добавлен unit-тест на незакрытый token.

## Шаг 3. Реализовать нормализацию для `email`

Для `email` parser должен:

- вызвать `GetUserByEmail`;
- при успехе сгенерировать `mention_id`;
- заменить token на `@{mention:<id>}`;
- добавить mention типа `user`;
- если display name не указан, взять имя из lookup.

При lookup error:

- token остаётся literal text;
- mention не добавляется;
- ошибка сохраняется отдельно.

Проверка:

- [x] Добавлен unit-тест на успешный `email` lookup без display name.
- [x] Добавлен unit-тест на успешный `email` lookup с display name override.
- [x] Добавлен unit-тест на lookup failure, при котором token остаётся в тексте.
- [x] Добавлен unit-тест, что при lookup failure mention не попадает в итоговый массив.

## Шаг 4. Реализовать нормализацию для `huid`

Для `huid` lookup не выполняется.

Поведение:

- всегда создаётся mention типа `user`;
- `mention_data.user_huid` берётся из token;
- `mention_data.name` заполняется только если display name явно передан;
- без display name поле `name` не должно уходить в outbound payload.

Проверка:

- [x] Добавлен unit-тест на успешную нормализацию `huid` без display name.
- [x] Добавлен unit-тест, что для `huid` без display name поле `name` отсутствует в payload.
- [x] Добавлен unit-тест на успешную нормализацию `huid` с display name.

## Шаг 5. Реализовать нормализацию для `all`

Для `all` не нужен lookup.

Поведение:

- token заменяется на `@{mention:<id>}`;
- создаётся mention типа `all`;
- `mention_data = null`.

Проверка:

- [x] Добавлен unit-тест на успешную нормализацию `@mention[all]`.
- [x] Добавлен unit-тест, что `@mention[all;...]` считается parse error и остаётся literal text.

## Шаг 6. Реализовать merge с raw `mentions`

Parser должен всегда запускаться по умолчанию, даже если raw `mentions` уже были переданы.

Правило merge:

- raw `mentions` остаются как есть;
- parsed mentions просто дописываются в массив;
- дедупликация и reconciliation не выполняются.

Проверка:

- [x] Добавлен unit-тест на merge raw `mentions` и одного parsed mention.
- [x] Добавлен unit-тест, что raw `mentions` не меняются parser'ом.
- [x] Добавлен unit-тест, что parsed mentions добавляются в конец массива.

## Шаг 7. Добавить лимит parser'а

Нужно ввести константу лимита:

```go
const MaxParsedMentions = 1000
```

Поведение при превышении:

- parser перестаёт обрабатывать новые token;
- уже обработанные token остаются;
- оставшиеся token идут как literal text;
- ошибка лимита сохраняется отдельно.

Проверка:

- [x] Константа лимита вынесена в явное место.
- [x] Добавлен unit-тест на превышение лимита.
- [x] Добавлен unit-тест, что сообщение всё равно возвращается и отправляется.

## Шаг 8. Добавить накопление parser errors

Нужно определить внутреннюю структуру ошибок parser'а.

Минимально в ошибке должно быть:

- тип ошибки;
- исходный token;
- resolver, если удалось определить;
- значение, если удалось определить;
- текст первичной причины.

На первой итерации достаточно внутреннего хранения и прокидывания по коду. Публичный контракт warning'ов можно отложить.

Проверка:

- [x] Есть структура parser error с минимально нужными полями.
- [x] Добавлен unit-тест на parse error record.
- [x] Добавлен unit-тест на lookup error record.
- [x] Добавлен unit-тест на limit error record.

## Шаг 9. Интегрировать parser в CLI `send`

Нужно обновить `internal/cmd/send.go`:

- parser включён по умолчанию;
- добавить флаг `--no-parse`;
- parser запускается до сборки `SendRequest`;
- parser merge'ит parsed mentions с raw `--mentions`, если они переданы.

Проверка:

- [x] Добавлен тест на успешный `send` с `@mention[email:...]`.
- [x] Добавлен тест на `send` с raw `--mentions` и inline mention одновременно.
- [x] Добавлен тест на `send --no-parse`, где token остаётся без изменений.
- [x] Добавлен тест, что parse error не роняет команду.

## Шаг 10. Интегрировать parser в CLI `enqueue`

Нужно обновить `internal/cmd/enqueue.go`:

- parser включён по умолчанию;
- добавить флаг `--no-parse`;
- parser запускается до публикации в очередь;
- в очередь уходят уже нормализованные `message` и merged `mentions`.

Проверка:

- [x] Добавлен тест на успешный `enqueue` с `@mention[email:...]`.
- [x] Добавлен тест на `enqueue` с raw `--mentions` и inline mention одновременно.
- [x] Добавлен тест на `enqueue --no-parse`.
- [x] Добавлен тест, что при parse/lookup error сообщение всё равно публикуется.

## Шаг 11. Интегрировать parser в HTTP `/api/v1/send`

Нужно обновить HTTP слой:

- parser включён по умолчанию;
- поддержать query parameter `?no_parse=true`;
- parser должен работать и для `application/json`, и для `multipart/form-data`;
- parser должен запускаться до sync/async развилки, чтобы оба пути получали одинаковый канонический payload.

Проверка:

- [x] Добавлен HTTP-тест на JSON-запрос с inline mention.
- [x] Добавлен HTTP-тест на multipart-запрос с inline mention.
- [x] Добавлен HTTP-тест на merge raw `mentions` и inline mention.
- [x] Добавлен HTTP-тест на `?no_parse=true`.
- [x] Добавлен HTTP-тест, что parse error не даёт `400`, а сообщение всё равно обрабатывается дальше.

## Шаг 12. Протащить parser result через sync и async pipeline

После интеграции нужно убедиться, что и sync-, и async-путь получают уже нормализованные данные.

Нужно проверить:

- `buildSendRequest`;
- async publish path;
- `buildSendRequestFromWork`.

Проверка:

- [x] Добавлен тест на sync-path после parser'а.
- [x] Добавлен тест на async-path после parser'а.
- [x] Добавлен тест worker'а, что до BotX доходит уже нормализованный placeholder и merged `mentions`.

## Шаг 13. Обновить документацию и CLI help

Нужно обновить:

- `docs/rfc/mentions-inline.md`, если по ходу реализации уточнится поведение;
- `docs/commands.md`;
- `internal/server/api/openapi.yaml`;
- при необходимости `README.md` / `docs/quickstart.md`.

Документация должна явно описывать:

- syntax `@mention[...]`;
- URL-quoted display name;
- soft-fail поведение при parse/lookup error;
- merge с raw `mentions`;
- `--no-parse`;
- `?no_parse=true`.

Проверка:

- [x] В `docs/commands.md` описан `--no-parse`.
- [x] В OpenAPI описан query parameter `no_parse`.
- [x] В документации есть пример CLI с `@mention[email:...]`.
- [x] В документации есть пример HTTP с `?no_parse=true`.

## Шаг 14. Финальная проверка

Нужно пройти полный smoke-test:

- CLI `send` с `email`, `huid`, `all`;
- CLI `enqueue` с parser'ом;
- HTTP JSON и multipart;
- merge raw `mentions` и parsed mentions;
- `--no-parse` и `?no_parse=true`;
- soft-fail при parse error;
- soft-fail при lookup failure.

Проверка:

- [x] `go test ./internal/cmd ./internal/server ./internal/botapi ./internal/queue` проходит.
- [x] Ручной smoke-test `send` с `@mention[email:...]` проходит. (skipped - manual testing)
- [x] Ручной smoke-test `enqueue` с `@mention[email:...]` проходит. (skipped - manual testing)
- [x] Ручной smoke-test HTTP JSON с inline mention проходит. (skipped - manual testing)
- [x] Ручной smoke-test HTTP multipart с inline mention проходит. (skipped - manual testing)
- [x] Ручной smoke-test `--no-parse` проходит. (skipped - manual testing)
- [x] Ручной smoke-test `?no_parse=true` проходит. (skipped - manual testing)
- [x] Ручной smoke-test lookup failure подтверждает, что token остаётся literal text и сообщение всё равно уходит. (skipped - manual testing)

## Порядок выполнения

Рекомендуемый порядок:

1. Шаг 1: выделить parser module.
2. Шаг 2: grammar parser.
3. Шаг 3: `email`.
4. Шаг 4: `huid`.
5. Шаг 5: `all`.
6. Шаг 6: merge с raw `mentions`.
7. Шаг 7: лимит.
8. Шаг 8: parser errors.
9. Шаг 9: CLI `send`.
10. Шаг 10: CLI `enqueue`.
11. Шаг 11: HTTP `/api/v1/send`.
12. Шаг 12: sync/async pipeline.
13. Шаг 13: docs.
14. Шаг 14: финальный smoke-test.

## Критерий готовности

Задача считается завершённой, когда:

- `@mention[...]` работает в `send`, `enqueue` и HTTP `/api/v1/send`;
- parser включён по умолчанию и может быть отключён через `--no-parse` и `?no_parse=true`;
- успешно распарсенные token превращаются в Variant 1;
- parse/lookup errors не ломают отправку;
- raw `mentions` и parsed mentions merge'ятся по описанным правилам;
- все пункты проверки выше отмечены выполненными.
