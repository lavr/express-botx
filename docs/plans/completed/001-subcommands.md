# RFC-001: Расширение субкоманд CLI-утилиты express-botx

- **Статус:** Draft
- **Дата:** 2026-03-13

## Контекст

Сейчас утилита поддерживает две команды:

- **`send`** — отправка текстового сообщения в чат
- **`chats list`** — список чатов, в которых состоит бот

Анализ 16 готовых ботов и 4 SmartApp из каталога eXpress показал, что практически все они используют общий набор операций BotX API, которые имеет смысл вынести в CLI для автоматизации и скриптинга.

## Обратная совместимость

Обратная совместимость не требуется. Текущий интерфейс (`send`, `chats list`) может быть заменён новой иерархией команд без сохранения алиасов.

## Иерархия субкоманд

```
express-botx
├── send
│   ├── message     # отправка текста (текущий send)
│   └── file        # отправка файла
├── chats
│   ├── list        # список чатов бота (текущий chats list)
│   ├── info        # детали конкретного чата
│   ├── create      # создание чата
│   └── members
│       ├── add     # добавить участников
│       └── remove  # убрать участников
├── message
│   ├── edit        # редактирование сообщения
│   └── delete      # удаление сообщения
├── user
│   └── search      # поиск пользователя по email/huid/ad-login
├── bot
│   ├── info        # информация о боте
│   └── ping        # проверка доступности
```

## Описание субкоманд

### `send message` — отправка текстового сообщения

Текущая команда `send`, перенесённая в новую иерархию.

```bash
express-botx send message "Деплой завершён"
express-botx send message --chat-id UUID --from report.txt
echo "alert" | express-botx send message
```

**Изменение флагов:** текущий флаг `--message-from` переименовывается в `--from`. Старое имя не сохраняется — обратная совместимость не требуется (см. раздел выше).

### `send file` — отправка файла в чат

**Обоснование:** Почта Бот, Forwarder Bot, Service Desk Bot — все работают с вложениями. Для CI/CD-пайплайнов (отчёты, артефакты сборки) отправка файлов критична.

```bash
express-botx send file --chat-id UUID ./report.pdf
express-botx send file --chat-id UUID --caption "Отчёт за март" ./report.pdf
cat archive.tar.gz | express-botx send file --chat-id UUID --filename archive.tar.gz
```

**Флаги:**

| Флаг | Описание |
|------|----------|
| `--caption` | Текст-подпись к файлу |
| `--filename` | Имя файла (для stdin) |

### `chats list` — список чатов бота

Существующая команда `chats list`, без изменений в интерфейсе.

```bash
express-botx chats list
express-botx chats list --format json
```

### `chats info` — детальная информация о конкретном чате

**Обоснование:** для скриптов часто нужны конкретные данные: список участников, тип чата, описание.

```bash
express-botx chats info --chat-id UUID
express-botx chats info --chat-id UUID --format json
```

**Вывод:** UUID, имя, тип, описание, количество и список участников (HUID).

### `chats create` — создание чата

**Обоснование:** Invite Bot, Random Coffee Bot создают чаты программно. Полезно для автоматизации: создание инцидент-каналов, проектных чатов.

```bash
express-botx chats create --name "Инцидент #1234" --members "uuid1,uuid2,uuid3"
express-botx chats create --name "Релиз 2.0" --description "Обсуждение релиза" --members "uuid1"
```

**Флаги:**

| Флаг | Описание | Обязательный |
|------|----------|:---:|
| `--name` | Название чата | да |
| `--description` | Описание чата | нет |
| `--members` | Список HUID через запятую | да |

**Вывод:** UUID созданного чата.

### `chats members add` / `chats members remove` — управление участниками

**Обоснование:** Invite Bot, Onboarding Bot — управление составом чатов. Для автоматизации онбординга/офбординга через скрипты.

```bash
express-botx chats members add --chat-id UUID --huids "uuid1,uuid2"
express-botx chats members remove --chat-id UUID --huids "uuid1"
```

### `message edit` — редактирование отправленного сообщения

**Обоснование:** используется в ботах для обновления статусных сообщений (Service Desk Bot, Workload Monitoring). В CI/CD — обновление сообщения о прогрессе деплоя.

```bash
express-botx message edit --chat-id UUID --sync-id MSG_UUID "Новый текст"
```

### `message delete` — удаление сообщения

**Обоснование:** удаление устаревших уведомлений.

```bash
express-botx message delete --chat-id UUID --sync-id MSG_UUID
```

### Открытый вопрос: источник `sync_id`

Команды `message edit` и `message delete` требуют `sync_id` сообщения. Текущая реализация `SendNotification` не возвращает идентификатор: на HTTP 202 тело ответа не читается (`client.go:119-121`), а на HTTP 200 из ответа извлекается только `status`.

Для реализации `message edit` / `message delete` необходимо решить одно из:

1. **Изменить `SendNotification`** — парсить тело ответа и извлекать `sync_id` (если BotX API его возвращает в обоих случаях — 200 и 202). Требуется проверка документации API.
2. **Генерировать `sync_id` на клиенте** — если BotX API поддерживает клиентские идентификаторы (`sync_id` передаётся в запросе), генерировать UUID v4 на стороне CLI и передавать при отправке.
3. **Отложить `message edit` / `message delete`** до прояснения контракта API.

До решения этого вопроса команды `message edit` и `message delete` остаются в статусе P2 и не должны браться в работу.

### `user search` — поиск пользователя

**Обоснование:** GetInfo Bot и Contacts Bot целиком построены вокруг поиска пользователей. Для скриптов — получение HUID по email/логину для дальнейшей отправки.

```bash
express-botx user search --email ivan@company.ru
express-botx user search --huid UUID
express-botx user search --ad-login ivanov
```

**Вывод:** HUID, имя, email, AD-логин, подразделение, должность.

### `bot info` — информация о боте

```bash
express-botx bot info
```

**Вывод:** Bot ID, Host, режим кэша, статус аутентификации (ok/error). Токен и его части не выводятся — команда будет использоваться в CI и support-логах.

### `bot ping` — проверка доступности бота

**Обоснование:** Healthcheck Bot построен вокруг проверки живости. Для мониторинга (Zabbix, Prometheus) нужна простая команда с exit code.

```bash
express-botx bot ping          # статус, время ответа
express-botx bot ping --quiet  # только exit code
```

**Контракт проверки:** команда выполняет два шага:

1. **Аутентификация** — получение токена через `GET /api/v2/botx/bots/{bot_id}/token` (auth.GetToken).
2. **BotX-запрос** — `GET /api/v3/botx/chats/list` (botapi.ListChats) как минимальный запрос без побочных эффектов.

Время ответа (`latency`) считается от начала шага 1 до завершения шага 2 — это полный путь DNS → TLS → auth → BotX API.

- `exit 0` — оба шага успешны
- `exit 1` — ошибка на любом шаге (вывод указывает, на каком именно)

Если аутентификация проходит, но BotX-запрос падает — это **не** "зелёный" статус. Мониторинг должен видеть реальную доступность рабочих endpoint'ов, а не только валидность токена.

## Общие улучшения

### Флаг `--format` для всех команд с выводом

```
--format text   # человекочитаемый (по умолчанию)
--format json   # для jq / скриптов
```

## Приоритизация

| Приоритет | Команда | Сложность | Ценность |
|-----------|---------|-----------|----------|
| P0 | `send file` | Средняя | Высокая — CI/CD, отчёты |
| P0 | `bot ping` | Низкая | Высокая — мониторинг |
| P1 | `chats info` | Низкая | Средняя — скрипты |
| P1 | `bot info` | Низкая | Средняя — выделение из chats list |
| P1 | `user search` | Средняя | Средняя — автоматизация |
| P1 | `--format json` | Низкая | Средняя — скрипты |
| P2 | `chats create` | Средняя | Средняя — автоматизация |
| P2 | `message edit` | Низкая | Средняя — зависит от sync_id |
| P2 | `message delete` | Низкая | Низкая — зависит от sync_id |
| P2 | `chats members add/remove` | Средняя | Средняя |

## Изменения в архитектуре

1. **`internal/botapi/client.go`** — добавить методы: `UploadFile`, `ChatInfo`, `SearchUser`, `CreateChat`, `EditNotification`, `DeleteNotification`, `AddMembers`, `RemoveMembers`.
2. **`main.go`** — рефакторинг: вынести маршрутизацию команд в отдельный пакет `internal/cmd/` (сейчас всё в одном `main.go`, при 10+ командах станет неуправляемым).
3. *(conditional, зависит от решения открытого вопроса по sync_id)* **`send message`** — изменить `SendNotification`, чтобы возвращать `sync_id`. Не брать в работу до подтверждения того, что BotX API возвращает `sync_id` в ответе на отправку уведомления.
