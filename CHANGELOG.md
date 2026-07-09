# Changelog

## 0.34.0

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
