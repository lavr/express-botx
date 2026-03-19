# RFC-002: Именованные чаты (chat aliases) в конфигурации

- **Статус:** Draft
- **Дата:** 2026-03-13

## Контекст

Сейчас для отправки сообщения нужно указывать UUID чата:

```bash
express-botx send message --chat-id 7ee8aaa9-c6cb-5ee6-8445-7d654819b285 "Деплой OK"
```

UUID невозможно запомнить, легко перепутать и неудобно использовать в скриптах. В конфиге можно указать только один `chat_id` — при работе с несколькими чатами приходится каждый раз передавать UUID через флаг.

Типичные сценарии, требующие нескольких чатов:

- **CI/CD**: уведомления о сборке в один чат, о деплое — в другой
- **Мониторинг**: алерты в дежурный чат, дайджесты — в общий
- **Скрипты**: отчёты в разные проектные чаты

## Предложение

Добавить в конфигурационный файл секцию `chats` — словарь именованных чатов (алиасов). Флаг `--chat-id` принимает как UUID, так и имя из конфига.

### Формат конфигурации

```yaml
host: express.company.ru
bot_id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
secret: my-bot-secret

# Чат по умолчанию (обратная совместимость)
chat_id: 7ee8aaa9-c6cb-5ee6-8445-7d654819b285

# Именованные чаты
chats:
  deploy:    7ee8aaa9-c6cb-5ee6-8445-7d654819b285
  alerts:    a1b2c3d4-e5f6-7890-abcd-ef1234567890
  reports:   f40f109f-3c15-577c-987a-55473e7390d1
```

### Использование

```bash
# По имени из конфига
express-botx send message --chat-id deploy "Деплой завершён"
express-botx send message --chat-id alerts "CPU > 90%"
express-botx send file --chat-id reports ./report.pdf

# UUID по-прежнему работает
express-botx send message --chat-id 7ee8aaa9-c6cb-5ee6-8445-7d654819b285 "Hello"

# Без --chat-id — берётся chat_id из конфига (как сейчас)
express-botx send message "Hello"

# chats info по имени
express-botx chats info --chat-id deploy
```

### Правила резолва `--chat-id`

1. Если значение — валидный UUID (формат `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`) → использовать как есть
2. Иначе — искать в секции `chats` конфига
3. Если не найден — ошибка с перечислением доступных имён
4. Если `--chat-id` не указан — использовать `chat_id` из конфига (текущее поведение)

### Пример ошибки

```
error: unknown chat alias "deploi", available: alerts, deploy, reports
```

## Изменения

### `internal/config/config.go`

```go
type Config struct {
    Host   string            `yaml:"host"`
    BotID  string            `yaml:"bot_id"`
    Secret string            `yaml:"secret"`
    ChatID string            `yaml:"chat_id"`
    Chats  map[string]string `yaml:"chats"`  // name → UUID
    Cache  CacheConfig       `yaml:"cache"`
    Format string            `yaml:"-"`
}
```

Метод `ResolveChatID()` заменяет текущий `RequireChatID()`:

```go
// ResolveChatID resolves ChatID: if it's a UUID, use as-is;
// if it matches a name in Chats map, substitute the UUID;
// if empty, keep as-is (for RequireChatID to handle later).
func (c *Config) ResolveChatID() error {
    if c.ChatID == "" {
        return nil
    }
    if isUUID(c.ChatID) {
        return nil
    }
    uuid, ok := c.Chats[c.ChatID]
    if !ok {
        names := sortedKeys(c.Chats)
        return fmt.Errorf("unknown chat alias %q, available: %s",
            c.ChatID, strings.Join(names, ", "))
    }
    c.ChatID = uuid
    return nil
}
```

`RequireChatID()` вызывает `ResolveChatID()` внутри себя — все существующие вызовы продолжат работать без изменений.

### `internal/cmd/` — без изменений в коде команд

Все команды уже вызывают `cfg.RequireChatID()` или используют `cfg.ChatID` напрямую. Резолв алиаса происходит прозрачно внутри `RequireChatID()`.

### Переменная окружения

`EXPRESS_CHAT_ID` принимает как UUID, так и имя алиаса (резолв по тем же правилам).

### `chats alias` — управление алиасами из CLI (опционально, P1)

Для удобства — CRUD алиасов без ручного редактирования YAML:

```bash
# Добавить / обновить алиас
express-botx chats alias set deploy 7ee8aaa9-c6cb-5ee6-8445-7d654819b285

# Список алиасов
express-botx chats alias list

# Удалить алиас
express-botx chats alias rm deploy
```

Эти команды читают и перезаписывают конфиг-файл. Требуют `--config` (или дефолтный путь).

## Обратная совместимость

Полная. Конфиг без секции `chats` работает как раньше. Поле `chat_id` сохраняется и является фолбэком, когда `--chat-id` не передан.

## Приоритизация

| Приоритет | Задача | Сложность |
|-----------|--------|-----------|
| P0 | Секция `chats` в конфиге + резолв в `RequireChatID()` | Низкая |
| P0 | Сообщение об ошибке с перечислением доступных алиасов | Низкая |
| P1 | `chats alias set/list/rm` | Средняя |

## Файлы

| Действие | Файл |
|----------|------|
| MODIFY | `internal/config/config.go` — поле `Chats`, `ResolveChatID()`, обновлённый `RequireChatID()` |
| MODIFY | `README.md` — документация по `chats` в конфиге |

## Проверка

1. Конфиг без `chats` — работает как раньше
2. `--chat-id UUID` — UUID используется как есть
3. `--chat-id deploy` — резолвится в UUID из конфига
4. `--chat-id unknown` — ошибка с перечислением доступных имён
5. `EXPRESS_CHAT_ID=deploy` — резолвится аналогично
6. Команды `send message`, `send file`, `chats info` — все работают с алиасами
