# RFC-018: Массовый импорт чатов в конфиг

- **Статус:** Proposed
- **Дата:** 2026-03-17

## Контекст

Сейчас в CLI есть два соседних сценария:

- `express-botx chats list` показывает все чаты, в которых состоит бот
- `express-botx config chat add` добавляет в конфиг один чат за раз

Это неудобно, когда нужно быстро занести в конфиг все чаты бота. Пользователь сначала получает список:

```bash
express-botx chats list --config config-local.yaml
```

А затем вынужден либо:

- вручную вызывать `config chat add` / `config chat set` для каждого UUID
- либо редактировать YAML руками

Для типичного bootstrap-сценария это слишком медленно: у бота уже есть 5-20 чатов, и нужен способ одним действием перенести их в `chats:` секцию конфига.

## Предложение

Добавить новую команду:

```bash
express-botx config chat import [options]
```

Это окончательный CLI-интерфейс для bulk-flow. Режим `config chat add --all` не рассматривается.

Команда должна:

1. Аутентифицироваться как `chats list`
2. Получить список чатов через BotX API
3. Сгенерировать алиасы
4. Добавить отсутствующие записи в `chats:` секцию YAML
5. Напечатать сводку: что добавлено, что пропущено, где были конфликты

Это отдельная команда, а не расширение `config chat add`, потому что:

- `add` уже означает "добавить один чат"
- массовый импорт требует других флагов и другой модели ошибок
- у bulk-операции должен быть свой `--dry-run` и своё conflict-handling поведение

## Использование

### Базовый сценарий

```bash
express-botx config chat import --config config-local.yaml
```

Пример вывода:

```text
Imported chats: 4
  added:   web-admins -> 47694792-1263-5e54-9214-e92ed1e609be
  added:   tm-cache-alert -> 5de954e5-4853-5a18-af55-19f0b34ba360
  added:   arm-ci-cd -> 7ee8aaa9-c6cb-5ee6-8445-7d654819b285
```

По умолчанию импортируются только чаты типа `group_chat`.

### С привязкой к конкретному боту

```bash
express-botx config chat import --config config-local.yaml --bot deploy-bot
```

Все импортированные записи сохраняются как:

```yaml
chats:
  web-admins:
    id: 47694792-1263-5e54-9214-e92ed1e609be
    bot: deploy-bot
```

### Dry run

```bash
express-botx config chat import --config config-local.yaml --dry-run
```

Команда не пишет файл, а только показывает, какие записи были бы добавлены.

### Фильтрация

```bash
express-botx config chat import --config config-local.yaml --only-type group_chat
```

Это явное значение по умолчанию.

Для импорта конференций нужен отдельный вызов:

```bash
express-botx config chat import --config config-local.yaml --only-type voex_call
```

## Генерация алиасов

Новая команда требует детерминированной генерации алиасов из имени чата.

Текущее `slugify()` в `internal/cmd/chats.go` поддерживает только ASCII и для кириллицы может вернуть пустую строку. Для bulk-импорта это неприемлемо, потому что реальные имена чатов часто русскоязычные:

- `Веб-админы`
- `АРМ ci/cd`

### Требования к алиасам

1. Алиас должен быть стабильным при повторном импорте
2. Алиас должен быть читаемым по возможности
3. Алиас не должен получаться пустым
4. Коллизии должны разрешаться детерминированно

### Предлагаемое поведение

1. Сначала прогонять имя через улучшенный slugify:
   - латиница и цифры сохраняются
   - разделители и пробелы превращаются в `-`
   - кириллица полноценно транслитерируется в ASCII через готовую библиотеку
2. Если после нормализации строка пустая, использовать fallback:

```text
chat-47694792
```

то есть префикс `chat-` плюс первые 8 символов UUID.

3. Если алиас уже занят другим UUID, добавлять суффикс:

```text
web-admins-2
web-admins-3
```

или, если конфликтует уже существующая запись в конфиге, завершать импорт ошибкой по умолчанию.

### Готовые библиотеки

Рассмотренные варианты:

1. `github.com/mehanizm/iuliia-go`
   - сильная сторона: специализированная библиотека именно для кириллицы, с готовыми схемами и правилами сочетаний/окончаний
   - сильная сторона: zero-deps, tagged/stable module
   - ограничение: в основном ориентирована на русский/узбекский наборы и схемы транслитерации
2. `github.com/censync/go-translit`
   - сильная сторона: общий API по ISO 639-1, умеет много письменностей, в том числе кириллицу
   - сильная сторона: сразу возвращает ASCII-совместимый результат
   - ограничение: библиотека широкого профиля, для наших alias-задач выглядит менее предсказуемой, чем специализированный вариант
3. `github.com/gosimple/unidecode`
   - сильная сторона: очень простой API
   - ограничение: это generic unicode-to-ASCII approximation, а не специализированная кириллическая транслитерация; для читаемых alias годится как fallback, но не как основной механизм

Итоговый выбор: `github.com/mehanizm/iuliia-go` как основной движок транслитерации.

Почему:

1. Библиотека специализирована именно на кириллице, а это наш основной кейс.
2. У неё есть готовые схемы и правила не только для посимвольного маппинга, но и для сочетаний/окончаний.
3. API простой и без лишних зависимостей.

Для alias это лучше, чем:

- `go-translit`, который шире по охвату письменностей, но для нашего узкого кейса менее предсказуем
- `unidecode`, который хорош как generic ASCII approximation, но слишком груб как основной механизм кириллической транслитерации

`chat-<uuid8>` остаётся резервным fallback на случай пустого результата после нормализации.

## Правила merge и конфликты

По умолчанию команда должна быть безопасной и не перетирать существующий конфиг молча.

### Поведение по умолчанию

- если `alias` отсутствует в конфиге и UUID ещё не импортирован: добавить
- если `alias` уже указывает на тот же UUID: пропустить как already exists
- если тот же UUID уже есть под другим alias: пропустить и показать существующий alias
- если `alias` занят другим UUID: ошибка

### Дополнительные флаги

```bash
--dry-run
--only-type group_chat|voex_call
--prefix team-
--skip-existing
--overwrite
```

Правила:

- `--skip-existing`: конфликтующие alias не считаются ошибкой, а пропускаются
- `--overwrite`: существующий alias можно переписать новым UUID
- `--overwrite` и `--skip-existing` взаимоисключающие

`--default` в массовом импорте не поддерживается: default-чат должен выставляться отдельной явной командой, чтобы не делать неочевидный выбор автоматически.

## Формат конфигурации

Используется существующий `ChatConfig`, без изменений формата:

```yaml
chats:
  web-admins: 47694792-1263-5e54-9214-e92ed1e609be

  tm-cache-alert:
    id: 5de954e5-4853-5a18-af55-19f0b34ba360
    bot: deploy-bot
```

Если `--bot` не задан и `default: false`, сохраняется короткая форма. Это уже обеспечивается текущим `MarshalYAML()`.

## Изменения

### `internal/cmd/config.go`

Расширить `config chat` новой подкомандой:

```text
import  Import all bot chats into config
```

Точки изменения:

- роутинг в `runConfigChat()`
- help в `printConfigChatUsage()`

### `internal/cmd/chats.go`

Добавить `runChatsImport(args []string, deps Deps) error`.

Логика:

1. Парсинг флагов:
   - глобальные auth/config flags
   - `--dry-run`
   - `--only-type`
   - `--prefix`
   - `--skip-existing`
   - `--overwrite`
2. `config.Load(flags)` для auth
3. `authenticate(cfg)`
4. `client.ListChats(...)`
5. Отфильтровать список: по умолчанию оставить только `group_chat`, либо применить `--only-type`
6. `config.LoadMinimal(flags)` для записи
7. Построение индексов:
   - `alias -> chat`
   - `uuid -> alias`
8. Генерация алиаса для каждого чата
9. Merge по правилам выше
10. `SaveConfig()` если не `--dry-run`
11. Печать сводки

### Вспомогательные функции в `internal/cmd/chats.go`

Добавить или выделить:

```go
func importableChats(chats []botapi.ChatInfo, onlyType string) []botapi.ChatInfo
func buildChatIDIndex(chats map[string]config.ChatConfig) map[string]string
func generateChatAlias(name, uuid, prefix string, taken map[string]struct{}) string
func slugifyChatAlias(name string) string
```

Отдельная функция генерации алиаса нужна, чтобы переиспользовать её в `config chat add` и убрать расхождение между single-chat и bulk-flow.

### `internal/cmd/chats.go` — доработка `config chat add`

После появления improved slugify команда `config chat add --name ...` должна использовать ту же функцию генерации алиаса, что и import.

Это уберёт текущую проблему, когда чат с кириллическим именем может дать пустой alias.

### `go.mod`

Добавить зависимость:

```text
github.com/mehanizm/iuliia-go
```

### `internal/config/config.go`

Формат YAML менять не требуется.

Возможно добавить вспомогательный метод:

```go
func (c *Config) ChatIDIndex() map[string]string
```

Но это не обязательно: индекс можно собрать локально в CLI-слое.

## Формат вывода

### Text

```text
Imported chats: 4
Skipped chats: 1

Added:
  web-admins         47694792-1263-5e54-9214-e92ed1e609be  (Веб-админы)
  tm-cache-alert     5de954e5-4853-5a18-af55-19f0b34ba360  (TM Cache Alert)

Skipped:
  express-conference f40f109f-3c15-577c-987a-55473e7390d1  already exists as express-conference
```

### JSON

```json
{
  "added": [
    {
      "alias": "web-admins",
      "id": "47694792-1263-5e54-9214-e92ed1e609be",
      "name": "Веб-админы",
      "type": "group_chat"
    }
  ],
  "skipped": [
    {
      "alias": "express-conference",
      "id": "f40f109f-3c15-577c-987a-55473e7390d1",
      "reason": "already_exists"
    }
  ],
  "dry_run": false
}
```

## Тесты

Добавить тесты в `internal/cmd/chats_test.go`.

### Позитивные

1. `config chat import --dry-run` не меняет файл
2. `config chat import` добавляет несколько чатов в пустой конфиг
3. `config chat import --bot mybot` сохраняет `bot: mybot`
4. `config chat import` без `--only-type` импортирует только `group_chat`
5. `config chat import --only-type voex_call` импортирует конференции
6. Повторный импорт тех же чатов не дублирует записи

### Конфликты

7. Существующий `alias -> same uuid` пропускается
8. Существующий `uuid -> другой alias` пропускается с понятным сообщением
9. Существующий `alias -> другой uuid` даёт ошибку
10. `--skip-existing` вместо ошибки пропускает конфликт
11. `--overwrite` переписывает конфликтующий alias

### Алиасы

12. Кириллическое имя даёт непустой alias
13. Пустой после нормализации alias уходит в fallback `chat-xxxxxxxx`
14. Коллизии имён разрешаются детерминированно
15. `config chat add --name` использует ту же генерацию алиасов

### Валидация флагов

16. `--overwrite` и `--skip-existing` вместе дают ошибку
17. Неизвестный `--only-type` даёт ошибку

## Документация

Обновить:

- `README.md` в таблице команд
- раздел про `config chat`
- примеры bulk bootstrap

Новый пример:

```bash
express-botx config chat import --config config-local.yaml --only-type group_chat
express-botx config chat list --config config-local.yaml
```

## Риски

| Приоритет | Риск | Почему |
| --- | --- | --- |
| P0 | Плохая генерация alias для кириллицы | импорт создаст нечитаемые или пустые имена |
| P0 | Неподходящая схема транслитерации | alias будут читаться не так, как ожидает команда |
| P0 | Молчаливое перетирание существующих alias | может сломать рабочий конфиг |
| P1 | Нестабильный alias при повторном импорте | ухудшит идемпотентность |
| P1 | Ошибочный явный импорт `voex_call` | в конфиг могут попасть сущности, которые не нужны для send-flow |

## Открытые вопросы

Открытых вопросов нет.

## План реализации

1. Добавить `config chat import` в роутинг и help.
2. Вынести генерацию alias в отдельную функцию и переиспользовать её в `config chat add`.
3. Реализовать bulk-import c dry-run и merge-правилами по умолчанию.
4. Добавить фильтрацию по типу чата и флаги обработки конфликтов.
5. Покрыть тестами импорт, конфликты, кириллицу и идемпотентность.
6. Обновить README и help-примеры.
