# Changelog

### Added: HTTPS/TLS serving с hot reload сертификата

- `serve` и `serve --enqueue` поддерживают opt-in HTTPS через YAML, env или
  `--tls-cert`/`--tls-key`; минимальная версия — TLS 1.2.
- Сертификат и ключ перечитываются по content hash без остановки listener; ошибка
  ротации не сбрасывает последнюю корректную пару.
- Helm умеет создать cert-manager Certificate или смонтировать существующий TLS
  Secret, переключает probes/backend port name на HTTPS и сохраняет прежний HTTP
  render при `tls.enabled=false`.

## 0.34.0

### ⚠️ Breaking: GitLab-конфигурация только через senders

`server.gitlab` теперь содержит только непустой `senders`. Каждый sender
владеет своими `name`, `secret`, `chats`, `events`, `error_events`, `templates`,
`template_files` и `routes`. Для доставки без routes используйте `chats`, а
для fallback при роутинге — явный последний `match: {}`.

`?chat_id=` обходит routes, но остаётся в скоупе sender'а; `?bot=` всегда
игнорируется. Запрос `?chat_id=` на чат вне скоупа — или на in-scope чат,
названный алиасом с bot binding, отличным от цели сендера, — возвращает `403`.
Все «проглоченные» события возвращают `200` с полем `reason`: `event filtered`,
`empty message` или `no route matched`.

`config validate` оффлайн зеркалит проверки старта `serve` для gitlab: алиас без
`id`, две цели в один UUID, `route[].chats` вне скоупа и конфликт bot binding —
ошибки; raw UUID / алиас без `bot` в multi-bot — предупреждения. Старт `serve`
отвергает те же конфигурации.

### ⚠️ Breaking: единый формат ответа (`MultiSendResponse`) на всех точках отправки

Все эндпоинты отправки (`/send`, `/alertmanager`, `/grafana`, `/gitlab`) и CLI
(`send`, `enqueue`) теперь возвращают **единый конверт** `MultiSendResponse` —
даже для одного чата:

```json
{"ok": true,
 "results": [{"chat": "a", "sync_id": "..."},
             {"chat": "b", "request_id": "...", "queued": true}],
 "errors":  [{"chat": "c", "error": "resolving chat: ..."}]}
```

Раньше `/send`/`/alertmanager`/`/grafana` отвечали `{"ok":true,"sync_id":"..."}`,
а `/gitlab` — то одиночной, то fan-out-формой. Клиентам, которым нужен прежний
контракт, следует остаться на версии `0.33.x`.

### Added: multi-chat fan-out (`chat_id` через запятую)

- `chat_id=a,b,c` (в теле `/send` или `?chat_id=a,b,c` у вебхуков и CLI)
  рассылает сообщение во **все** перечисленные чаты (fan-out, best-effort).
  Дубликаты схлопываются, порядок сохраняется, чат и бот резолвятся для каждого
  таргета отдельно; в `/send` inline-mentions парсятся резолвером каждого бота.
- **Коды ответа:** `200` — sync (доставлено ≥1 чата), `202` — async (enqueue),
  `502` — во все чаты не удалось. Request-level ошибки (битый JSON, пустой
  `chat_id`, невалидный `status`, неподдерживаемый media-type) сохраняют прежнюю
  форму `{"ok":false,"error":"..."}` с кодами `400`/`415`.
- **Async expand:** в режиме `serve --enqueue` (и в CLI `enqueue`) многочатовый
  `chat_id` раскрывается в N независимых сообщений в очереди (по одному на чат)
  для per-chat retry/ack без дублей; worker не изменён.

См. [docs/integrations.md](docs/integrations.md#мульти-чат-и-единый-ответ-multisendresponse).
