# Дизайн: конфигурируемые кнопки для Alertmanager + Grafana

Дата: 2026-07-07
Статус: утверждён (brainstorming), ожидает implementation-плана

## Цель

Добавить под сообщениями webhook-эндпоинтов `/alertmanager` и `/grafana`
интерактивные **bubble-кнопки** eXpress (BotX), ведущие по ссылке, собранной
из полей payload'а алерта (например «открыть алерт в Alertmanager UI», «открыть
дашборд в Grafana», «runbook»). Кнопка открывается в клиенте одним тапом, не
требует копирования ссылки и не засоряет чат.

Референс — GitHub PR #18 (`gitlab-webhook-support`), где кнопки Alertmanager уже
были прототипированы. Этот дизайн адаптирует идею под текущую ветку и расширяет
на Grafana, с более гибким конфигом кнопки.

## Не-цели (out of scope)

- Кнопки для GitLab-вебхука (роутинг GitLab — отдельный спек).
- Callback-кнопки (`handler: bot`, `command`, `data`) в вебхуках — их некому
  обрабатывать в alertmanager/grafana-хендлерах. В конфиге вебхуков `handler`
  всегда `client` (открыть ссылку). Типы для callback-кнопок в `botapi`
  портируются (см. ниже), но в webhook-конфиге не выставляются.
- `keyboard` (нижняя клавиатура) в webhook-хендлерах — не нужна для нотификаций.

## Архитектура

Три слоя, снизу вверх.

### 1. Слой botapi (полный порт из PR #18)

`internal/botapi/client.go`:

- Новые типы:
  ```go
  type ButtonMarkup [][]Button

  type Button struct {
      Command string          `json:"command,omitempty"`
      Label   string          `json:"label,omitempty"`
      Data    json.RawMessage `json:"data,omitempty"`
      Opts    *ButtonOpts     `json:"opts,omitempty"`
  }

  type ButtonOpts struct {
      Silent          *bool  `json:"silent,omitempty"`
      FontColor       string `json:"font_color,omitempty"`
      BackgroundColor string `json:"background_color,omitempty"`
      Align           string `json:"align,omitempty"`
      HSize           int    `json:"h_size,omitempty"`
      ShowAlert       bool   `json:"show_alert,omitempty"`
      AlertText       string `json:"alert_text,omitempty"`
      Handler         string `json:"handler,omitempty"`
      Link            string `json:"link,omitempty"`
  }
  ```
- Поля `Bubble ButtonMarkup` и `Keyboard ButtonMarkup` (json `bubble`/`keyboard`,
  `omitempty`) в `SendNotification`.
- Поля `Bubble`/`Keyboard` в `SendParams`; проброс в `BuildSendRequest` в обеих
  ветках сборки: основной notification и «file-only с metadata/mentions»
  (в условие file-only добавить `|| len(p.Bubble) > 0 || len(p.Keyboard) > 0`,
  иначе file-only сообщение с кнопками потеряет `notification`-блок).

### 2. Публичный send-путь (кнопки доступны и через `/send`)

- `server.SendPayload` (`internal/server/handler_send.go`): добавить
  `Bubble ButtonMarkup` и `Keyboard ButtonMarkup` (json `bubble`/`keyboard`).
  Тип — `botapi.ButtonMarkup` (пакет `server` уже импортирует `botapi`).
- Проброс через все точки, где `SendPayload`/`queue.Payload` превращается в
  `botapi.SendParams`:
  1. `internal/cmd/serve.go` → `buildSendRequest(p *server.SendPayload)` —
     синхронная отправка.
  2. `internal/cmd/serve.go` → путь enqueue (`runServeEnqueue`): перенос
     `Bubble`/`Keyboard` в `queue.Payload`.
  3. `internal/queue/queue.go` → `Payload`: новые поля `Bubble`/`Keyboard`
     типа `botapi.ButtonMarkup` (json `bubble`/`keyboard`, `omitempty`).
     Цикла нет: `botapi` не импортирует `queue` (а `queue` уже импортирует
     `config`, так что `config`→`queue` невозможен) — `queue`→`botapi` безопасен.
  4. `internal/cmd/worker.go` → `buildSendRequestFromWork` — реконструкция
     `SendParams` из очереди.

Это отдельные, легко забываемые точки — каждая покрывается тестом сериализации.

### 3. Рендер кнопок вебхуков (общий механизм)

Новый файл `internal/server/handler_buttons.go`:

- Конфиг-структура рантайма (общая для обоих эндпоинтов):
  ```go
  type WebhookButtonConfig struct {
      Label           string
      URLTemplate     *template.Template
      Align           string
      Silent          *bool
      FontColor       string
      BackgroundColor string
      HSize           int
      DefaultLabel    string // "Open Alertmanager" / "Open Grafana"
  }
  ```
- Helper:
  ```go
  func renderWebhookButtons(buttons []WebhookButtonConfig, payload any) botapi.ButtonMarkup
  ```
  Для каждой кнопки: исполнить `URLTemplate` над `payload`; пустой URL или
  ошибка шаблона → кнопку пропустить (лог `V1`), не роняя отправку сообщения.
  Собрать все кнопки в **один ряд** (`ButtonMarkup{row}`); если ряд пуст —
  вернуть `nil`.
- Маппинг полей в `botapi.ButtonOpts`:
  `Handler: "client"`, `Link: <rendered url>`,
  `Align` (default `"center"`), `Silent` (nil → `true`),
  `FontColor`/`BackgroundColor`/`HSize` — как заданы.
  `Label` (пусто → `DefaultLabel`).

Хендлеры `handler_alertmanager.go` и `handler_grafana.go` вызывают
`renderWebhookButtons(cfg.Buttons, webhook)` и кладут результат в
`SendPayload.Bubble`. Payload передаётся целиком (`AlertmanagerWebhook` /
`GrafanaWebhook`), так что шаблон видит все поля (`.ExternalURL`, `.Receiver`,
`.CommonLabels`, `.Status`, `.Alerts`, для Grafana также `.Title`, `.State`,
`.Message` и т.д.).

Конфиги эндпоинтов (`AlertmanagerConfig`, `GrafanaConfig`) получают поле
`Buttons []WebhookButtonConfig`.

## Конфиг (YAML)

```yaml
server:
  alertmanager:
    default_chat_id: alerts
    buttons:
      - label: "Alertmanager"
        url_template: "{{ .ExternalURL }}/#/alerts?receiver={{ .Receiver | urlquery }}"
      - label: "Grafana"
        url_template: "https://grafana.example.com/d/abc?var-alert={{ index .CommonLabels \"alertname\" | urlquery }}"
        align: left
        background_color: "#d64545"
  grafana:
    default_chat_id: alerts
    buttons:
      - label: "Открыть в Grafana"
        url_template: "{{ .ExternalURL }}"
        silent: false
        h_size: 2
```

YAML-структура (общая, в `internal/config/config.go`):

```go
type WebhookButtonYAMLConfig struct {
    Label           string `yaml:"label,omitempty"`
    URLTemplate     string `yaml:"url_template,omitempty"`
    Align           string `yaml:"align,omitempty"`
    Silent          *bool  `yaml:"silent,omitempty"`
    FontColor       string `yaml:"font_color,omitempty"`
    BackgroundColor string `yaml:"background_color,omitempty"`
    HSize           int    `yaml:"h_size,omitempty"`
}
```

- Поле `Buttons []WebhookButtonYAMLConfig` в `AlertmanagerYAMLConfig` и
  `GrafanaYAMLConfig`.
- `knownKeys`: добавить `buttons` в `server.alertmanager` и `server.grafana`;
  добавить наборы ключей `server.alertmanager.buttons.*` и
  `server.grafana.buttons.*` (`label`, `url_template`, `align`, `silent`,
  `font_color`, `background_color`, `h_size`).

### Дефолты

- `label` пусто → `"Open Alertmanager"` / `"Open Grafana"`.
- `align` пусто → `"center"`.
- `silent` nil → `true`.
- `url_template` пусто → кнопка **не создаётся** (пропускается на этапе сборки).
  Готового дефолтного URL нет: payload'ы AM и Grafana различны, а «угадать»
  корректную ссылку нельзя — ссылку задаёт пользователь явно.

## Сборка и валидация

- В `internal/cmd/serve.go`, `buildAlertmanagerConfig` / `buildGrafanaConfig`:
  для каждой кнопки с непустым `url_template` —
  1. `secret.Resolve(url_template)` (позволяет `env:`/`vault:` в базовом URL);
  2. `template.New(...).Parse(...)` → `*template.Template`; ошибка парсинга
     роняет `serve` (и `config validate` через тот же путь, если применимо).
  Кнопки с пустым `url_template` отбрасываются на этом шаге (в рантайм-конфиг
  не попадают).
- Новый helper `ParseWebhookButtonURLTemplate(tmplStr string) (*template.Template, error)`
  в `internal/server` (по аналогии с `ParseAlertmanagerTemplate`).
- Валидация в `config validate` (`internal/config/config.go`):
  - `align` не из {`""`,`left`,`center`,`right`} → `ValidationWarning`.
  - (URL-шаблон не парсим на этапе `config validate` без доступа к `server`-пакету;
    ошибка парсинга ловится при `serve`. Достаточно, т.к. `serve` — обязательный
    следующий шаг.)

## Обработка ошибок

- Ошибка исполнения `url_template` в рантайме → кнопка пропускается, сообщение
  всё равно отправляется (кнопки не должны ронять доставку алерта). Лог `V1`.
- Пустой отрендеренный URL → кнопка пропускается.
- Пустой итоговый ряд → `Bubble = nil`, отправляется обычное сообщение.

## Тестирование

- `internal/botapi/send_test.go`: `bubble`/`keyboard` корректно сериализуются в
  JSON `SendRequest` (в т.ч. file-only ветка с кнопками сохраняет `notification`).
- `internal/server/handler_buttons_test.go` (новый): рендер —
  одна кнопка; несколько кнопок в один ряд; пустой URL → пропуск; ошибка
  шаблона → пропуск, ряд пуст → nil; маппинг `align`/`silent`/цвета/`h_size`;
  дефолты label/align/silent.
- `handler_alertmanager_test.go` / `handler_grafana_test.go`: сообщение с
  кнопкой уходит в `SendPayload.Bubble`; без секции `buttons` — `Bubble` пуст.
- `internal/config/config_test.go`: `knownKeys` для новых путей; warning на
  плохой `align`.
- Проброс `/send`→очередь→worker: тест сериализации `queue.Payload` с кнопками
  и восстановления в `buildSendRequestFromWork`.

## Документация

- `docs/configuration.md`: секции `buttons` под `server.alertmanager` и
  `server.grafana` с примерами; описание полей и дефолтов; пометка, что кнопка
  всегда `handler: client` (открывает ссылку) и по умолчанию `silent`.
- Примеры — только нейтральные плейсхолдеры (`grafana.example.com`,
  `alertmanager.example.com`); без названий конкретных организаций.

## Затрагиваемые файлы

- `internal/botapi/client.go` — типы кнопок, поля `bubble`/`keyboard`.
- `internal/botapi/send_test.go` — тесты сериализации.
- `internal/server/handler_send.go` — поля в `SendPayload`.
- `internal/server/handler_buttons.go` (новый) — `WebhookButtonConfig`,
  `renderWebhookButtons`, `ParseWebhookButtonURLTemplate`.
- `internal/server/handler_buttons_test.go` (новый).
- `internal/server/handler_alertmanager.go` — вызов рендера, поле `Buttons`.
- `internal/server/handler_grafana.go` — вызов рендера, поле `Buttons`.
- `internal/config/config.go` — `WebhookButtonYAMLConfig`, поля `Buttons`,
  `knownKeys`, валидация `align`.
- `internal/cmd/serve.go` — сборка кнопок, проброс `Bubble`/`Keyboard` в
  sync- и enqueue-путях.
- `internal/queue/queue.go` — поля `Bubble`/`Keyboard` в `Payload`.
- `internal/cmd/worker.go` — проброс в `buildSendRequestFromWork`.
- `docs/configuration.md` — документация.
