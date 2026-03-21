# Команда `express-botx api`

Отправляет аутентифицированные HTTP-запросы к eXpress BotX API.
Автоматически получает и кэширует токен бота — не нужно вручную управлять авторизацией.

```
express-botx api [options] <endpoint>
```

`<endpoint>` — путь к API (должен начинаться с `/`).
HTTP-метод определяется автоматически: GET если нет тела запроса, POST если есть.

## Флаги

| Флаг | Тип | Описание |
|------|-----|----------|
| `-X, --method` | string | HTTP-метод (по умолчанию — автоопределение) |
| `-f, --field` | key=value | Строковое поле для JSON-тела (повторяемый) |
| `-F` | key=value | Типизированное поле: `true/false` → bool, число → number, `@file` → содержимое файла (повторяемый) |
| `-H, --header` | key:value | HTTP-заголовок (повторяемый) |
| `--input` | string | Файл с телом запроса (`-` для stdin, `@file` для multipart) |
| `--part-name` | string | Имя multipart-части для бинарного файла (по умолчанию `content`) |
| `-q, --jq` | string | jq-выражение для фильтрации JSON-ответа |
| `-i, --include` | bool | Показать HTTP-статус и заголовки ответа |
| `--timeout` | duration | Таймаут HTTP-запроса |
| `--silent` | bool | Не выводить тело ответа |

Глобальные флаги: `--config`, `--bot`, `--host`, `--bot-id`, `--secret`, `--token`, `--no-cache`, `--format`, `--verbose`.

## Режимы тела запроса

**JSON** (`-f`/`-F` без `--input @file`) — поля собираются в JSON-объект, `Content-Type: application/json` ставится автоматически.

**Raw** (`--input` без префикса `@`) — тело отправляется как есть. `Content-Type` нужно задать вручную через `-H`.

**Multipart** (`--input @file`, опционально с `-f`) — бинарный файл отправляется как часть с именем `content` (или другим через `--part-name`), текстовые поля из `-f` добавляются как дополнительные части.

## Примеры

### Получить список чатов бота

```bash
express-botx api /api/v3/botx/chats/list
```

### Создать чат

```bash
echo '{"name": "test", "chat_type": "group_chat", "members": ["<USER-HUID>"]}' \
  | express-botx api -X POST /api/v3/botx/chats/create \
      --input - -H 'Content-Type: application/json'
```

### Отправить уведомление в чат

```bash
express-botx api -X POST /api/v4/botx/notifications/direct \
  --input payload.json -H 'Content-Type: application/json'
```

### Фильтрация ответа через jq

```bash
express-botx api /api/v3/botx/chats/list -q '.result[].name'
```

### Типизированные поля

```bash
express-botx api -X POST /api/v3/botx/chats/stealth_set \
  -f group_chat_id=<UUID> \
  -F burn_in=60 \
  -F stealth=true
```

### Показать заголовки ответа

```bash
express-botx api -i /api/v3/botx/chats/list
```

## Доступные методы BotX API

### Аутентификация (Bots API)

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| GET | `/api/v2/botx/bots/:bot_id/token` | Получение токена бота |
| GET | `/api/v1/botx/bots/catalog` | Получение списка ботов сервера |

### Notifications API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| POST | `/api/v4/botx/notifications/direct` | Отправка уведомления в чат |
| POST | `/api/v4/botx/notifications/internal` | Отправка внутренней бот-нотификации |

### Chats API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| GET | `/api/v3/botx/chats/list` | Список чатов бота |
| GET | `/api/v3/botx/chats/info` | Информация о чате |
| POST | `/api/v3/botx/chats/create` | Создание чата |
| POST | `/api/v3/botx/chats/add_user` | Добавление пользователей в чат |
| POST | `/api/v3/botx/chats/remove_user` | Удаление пользователей из чата |
| POST | `/api/v3/botx/chats/add_admin` | Добавление администратора чата |
| POST | `/api/v3/botx/chats/stealth_set` | Включение стелс-режима |
| POST | `/api/v3/botx/chats/stealth_disable` | Отключение стелс-режима |
| POST | `/api/v3/botx/chats/pin_message` | Закрепление сообщения |
| POST | `/api/v3/botx/chats/unpin_message` | Открепление сообщения |

### Events API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| POST | `/api/v3/botx/events/edit_event` | Редактирование сообщения |
| POST | `/api/v3/botx/events/reply_event` | Ответ на сообщение (reply) |
| GET | `/api/v3/botx/events/:sync_id/status` | Статус сообщения |
| POST | `/api/v3/botx/events/typing` | Отправка индикатора набора |
| POST | `/api/v3/botx/events/stop_typing` | Остановка индикатора набора |
| POST | `/api/v3/botx/events/delete_event` | Удаление сообщения |

### Users API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| POST | `/api/v3/botx/users/by_email` | Поиск пользователей по email |
| GET | `/api/v3/botx/users/by_huid` | Поиск пользователя по HUID |
| GET | `/api/v3/botx/users/by_login` | Поиск пользователя по AD-логину |
| GET | `/api/v3/botx/users/by_other_id` | Поиск по дополнительному ID |
| GET | `/api/v3/botx/users/users_as_csv` | Список пользователей CTS (CSV) |
| PUT | `/api/v3/botx/users/update_profile` | Обновление профиля пользователя |

### Stickers API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| GET | `/api/v3/botx/stickers/packs` | Список наборов стикеров |
| GET | `/api/v3/botx/stickers/packs/:pack_id` | Информация о наборе стикеров |
| GET | `/api/v3/botx/stickers/packs/:pack_id/stickers/:sticker_id` | Получение стикера |
| POST | `/api/v3/botx/stickers/packs` | Создание набора стикеров |
| POST | `/api/v3/botx/stickers/packs/:pack_id/stickers` | Добавление стикера в набор |
| PUT | `/api/v3/botx/stickers/packs/:pack_id` | Редактирование набора стикеров |
| DELETE | `/api/v3/botx/stickers/packs/:pack_id` | Удаление набора стикеров |
| DELETE | `/api/v3/botx/stickers/packs/:pack_id/stickers/:sticker_id` | Удаление стикера |

### Metrics API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| POST | `/api/v3/botx/metrics/bot_function` | Логирование использования функционала |

### OpenID API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| POST | `/api/v3/botx/openid/refresh_access_token` | Обновление access_token |

### VoEx API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| GET | `/api/v3/botx/voex/conferences/:call_id` | Информация о конференции |
| GET | `/api/v3/botx/voex/calls/:call_id` | Информация о звонке |
