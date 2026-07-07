# Дизайн: правила роутинга GitLab-событий по чатам

Дата: 2026-07-08
Статус: утверждён (brainstorming), ожидает implementation-плана

## Цель

Дать эндпоинту `POST /api/v1/gitlab` конфигурируемый **роутинг событий по
чатам**: одно входящее GitLab-событие может уходить в один или несколько чатов
eXpress в зависимости от проекта, типа события, ветки и любых полей payload'а.
Сейчас (в универсальной реализации, которую пишет параллельно другой агент)
чат один: `?chat_id` → `default_chat_id` → global default → единственный чат.
Роутинг — надстройка поверх этой модели, закрывающая зазор «куда слать».

Инструмент проектируется как **универсальный опенсорс** — без привязки к
конкретной инсталляции GitLab.

## Не-цели (out of scope)

- Обобщение роутинга на `/alertmanager` и `/grafana`: их payload иной, а
  деривация event-key/branch — GitLab-специфична. Движок пишем изолированно,
  чтобы при желании вынести позже, но сейчас скоуп — только GitLab.
- Изменение аутентификации (`X-Gitlab-Token`), фильтра `only`/`exclude`,
  реестра шаблонов, `error_events` — это зона универсального GitLab-плана
  другого агента; роутинг с ними не конфликтует и не переписывает их.

## Модель роутинга

Опциональная секция `server.gitlab.routes` — **упорядоченный список правил**.
Каждое правило: `match` (условия), `chats` (цели), `stop` (флаг обрыва).

**Комбинирование правил:** срабатывают **все** совпавшие правила; их `chats`
объединяются в множество и **дедуплицируются**. Правило со `stop: true`, если
совпало, обрывает дальнейший перебор (гибрид all-match + first-match).

### Контекст матчинга

Единый контекст = сырые поля GitLab поверх которых лежат нормализованные.
Селектор в `match` резолвится так:

1. **Зарезервированные нормализованные ключи** (деривим/нормализуем сами):
   - `kind` — `object_kind` (glob).
   - `event` — event-key (`kind` или `kind.subtype`); матчится через
     `eventMatches` (тот же матчер, что `only`/`exclude`): запись равна полному
     event-key, ИЛИ голому `kind`, ИЛИ `kind.*`.
   - `action` — `object_attributes.action` (glob).
   - `project` — `project.path_with_namespace` (fallback `project.name`) (glob).
   - `branch` — нормализованная ветка события (см. ниже) (glob).
   - `user` — `user.name`/`user.username` (fallback `user_name`) (glob).
   - `title`, `url` — из нормализации (glob).
2. **Иначе** — произвольный **dotted-путь в Raw payload**
   (`object_attributes.detailed_merge_status`, `object_attributes.labels`,
   `object_attributes.work_in_progress`, …). Значение резолвится best-effort:
   скаляр → одно строковое значение; массив → список значений (матч, если
   **любой** элемент подходит под любой паттерн); отсутствует → пусто (условие
   не выполнено).

### Семантика match

- Внутри одного поля список паттернов — **OR** (any-of).
- Между полями — **AND** (все условия должны выполниться).
- Опущенное поле — «любое».
- Пустой `match: {}` (или отсутствие `match`) — **catch-all**.

### Паттерны

- **Glob** по умолчанию (`group/backend/*`, `sec:*`, `main`).
- Паттерн в виде `/regex/` — **регэксп** (Go `regexp`, движок RE2 → нет
  катастрофического бэктрекинга/ReDoS). Все регэкспы **компилируются на старте**;
  битый regex роняет конфиг.
- Ключ `event` — исключение: всегда матчится через `eventMatches`
  (glob/regex к нему не применяются), для консистентности с `only`/`exclude`.

### Нормализация `branch`

Значение ветки зависит от типа события (новый нормализатор):

| Событие | Источник |
|---|---|
| `merge_request`, `note` (по MR) | `object_attributes.target_branch` (fallback — `merge_request.target_branch`) |
| `push`, `tag_push` | basename `ref` (`refs/heads/main` → `main`) |
| `pipeline` | `object_attributes.ref` |
| прочие (issue, wiki, …) | пусто |

Требует добавления поля `Branch` в `gitlabView` (⚠️ координация — см. ниже).

## Приоритет назначения чатов

1. `?chat_id=` в URL → пиннит **один** чат, движок правил **пропускается**
   (сохраняет per-webhook-URL пиннинг).
2. Иначе — `routes`: объединённое дедуп-множество `chats` всех совпавших правил.
3. Если ни одно правило не совпало (или `routes` не задан) → `default_chat_id`
   → global default (`cfg.DefaultChatAlias`) → единственный чат (single-chat
   fallback). Если и их нет → `200 {"ok":true,"ignored":true}` без отправки.

`routes` полностью опционален: без него поведение = текущее (один чат).

## Фан-аут и отправка

- По каждому целевому чату из дедуп-множества:
  - резолв чата `s.chats(target)` → `chatResult` (ChatID + связанный бот);
  - резолв бота `resolveRequestBot(ctx, r.URL.Query().Get("bot"), chatResult.Bot)`
    (override `?bot=` действует на все цели);
  - `s.send(ctx, &SendPayload{...})`.
- **Best-effort**: собираем по-чатовые результаты, не прерываемся на первой
  ошибке.
- Ответ:
  - `200 {"ok":true,"results":[{"chat":"<alias/uuid>","sync_id":"…"}, …],
    "errors":[{"chat":"…","error":"…"}]}` — если доставлено ≥1;
  - `502` — если **все** отправки упали (GitLab ретрайнет; чаты, куда уже
    дошло, при ретрае не задублируются, т.к. они были бы в `results`, а не в
    `errors`).
- Пер-чатовые исходы логируются (`V1` успех/фейл с указанием чата и события).

## Изоляция кода

- **Новый файл `internal/server/gitlab_routing.go`**:
  - типы `GitlabRoute{ Match map[string][]string; Chats []string; Stop bool }`
    в рантайм-форме — с уже скомпилированными матчерами
    (`compiledRoute{ conds []condition; chats []string; stop bool }`,
    `condition{ selector string; matchers []patternMatcher }`);
  - `patternMatcher` — интерфейс glob/regex/eventMatches;
  - `buildMatchContext(view gitlabView) func(selector string) []string` —
    резолв нормализованных ключей и raw-путей;
  - `evaluateRoutes(routes []compiledRoute, view gitlabView) (chats []string,
    matched bool)` — перебор со `stop`, дедуп.
- **`handler_gitlab.go`** (другой агент): вместо одиночного резолва чата вызвать
  `evaluateRoutes`; реализовать multi-send и агрегированный ответ; добавить
  нормализацию `Branch` в `gitlabView`.
- **`config.go`**: расширить `GitlabYAMLConfig`.
- **`serve.go` `buildGitlabConfig`**: скомпилировать правила (glob/regex/event),
  провалидировать чаты, вернуть в `server.GitlabConfig`.

## Конфиг (YAML)

```yaml
server:
  gitlab:
    secret: env:GITLAB_TOKEN
    default_chat_id: fallback
    routes:
      - match:
          project: ["group/backend/*"]
          event:   ["pipeline.failed", "pipeline.success"]
          branch:  ["main", "/^release-/"]
          "object_attributes.labels": ["urgent"]
        chats: [ops, audit]
        stop: false
      - match:
          kind: ["merge_request"]
        chats: [dev]
```

YAML-структура:
```go
type GitlabRouteYAMLConfig struct {
    Match map[string][]string `yaml:"match,omitempty"`
    Chats []string            `yaml:"chats"`
    Stop  bool                `yaml:"stop,omitempty"`
}
// поле в GitlabYAMLConfig:
//   Routes []GitlabRouteYAMLConfig `yaml:"routes,omitempty"`
```

## Валидация (`config validate`)

- Каждое правило: `chats` непуст; каждый чат — существующий алиас или UUID
  (образец валидации `default_chat_id`).
- `event`-паттерны — валидный event-key синтаксис (`kind`, `kind.sub`, `kind.*`).
- Паттерны-регэкспы (`/…/`) — компилируются (иначе ошибка); glob — не требует
  проверки. Компиляция происходит и при `serve`; для `config validate`
  достаточно проверить regex-паттерны.
- Raw-путь-селекторы не валидируем (открытые), но их паттерны компилируем.
- `knownKeys`: `routes` в `server.gitlab`; вложенные ключи правила
  (`match`, `chats`, `stop`). `match` — открытая мапа (ключи-селекторы
  произвольны), поэтому её содержимое из проверки known-keys исключается.

## Обработка ошибок

- Битый glob/regex/event-паттерн → ошибка сборки (`serve`) / валидации.
- Несуществующий чат в правиле → ошибка валидации.
- Отсутствующее поле payload в условии → условие не выполнено (правило не
  совпадает), не ошибка.
- Все отправки упали → `502`; часть → `200` с `errors`.

## Тестирование

- `gitlab_routing_test.go` (новый):
  - матчинг: glob, `/regex/`, `event` через eventMatches, raw-путь (скаляр и
    массив labels), отсутствующее поле;
  - within-field OR, across-field AND, опущенное поле = any, пустой match =
    catch-all;
  - `evaluateRoutes`: несколько совпавших → объединение+дедуп; `stop` обрывает;
    ни одного совпадения → `matched=false`;
  - нормализация `branch` по типам событий (MR/push/tag/pipeline/без ветки).
- `handler_gitlab_test.go` (расширение): `?chat_id` обходит правила; фан-аут в
  2 чата → 2 `results`; частичный сбой → 200 + errors; все упали → 502; нет
  совпадений → default_chat_id; нет default → 200 ignored.
- `config_test.go`: known-keys для `routes`; валидация чатов/ event-ключей/
  regex; открытая `match`-мапа не ломает known-keys.
- `serve_gitlab_test.go`: компиляция правил (glob/regex/event), резолв чатов,
  ошибка на битом regex.

## Документация

- `docs/configuration.md` / `docs/integrations.md`: секция `routes` — модель
  (all-match + stop), контекст матчинга (нормализованные ключи + raw-пути),
  glob vs `/regex/`, нормализация `branch`, приоритет чатов, фан-аут и коды
  ответов. Примеры — только нейтральные плейсхолдеры (`group/backend/*`,
  `grafana.example.com`), без названий организаций.
- `examples/gitlab/`: конфиг с `routes` (несколько правил, фан-аут, stop).

## Координация с параллельной работой

⚠️ `internal/server/handler_gitlab.go`, `gitlabView`, `internal/config/config.go`,
`internal/cmd/serve.go` сейчас активно правит другой агент (универсальный
GitLab-приёмник, план `docs/plans/20260707-gitlab-events-universal.md`).
Роутинг **ложится поверх** его модели и требует:
- добавить нормализованное поле `Branch` в `gitlabView`;
- заменить одиночный резолв целевого чата в хендлере на вызов роутера +
  multi-send;
- расширить `GitlabYAMLConfig` полем `Routes` и `buildGitlabConfig` —
  компиляцией правил.

Исполнять роутинг-план следует **после** мержа универсального GitLab-приёмника,
либо согласовав точки соприкосновения, чтобы не переписывать друг друга.
