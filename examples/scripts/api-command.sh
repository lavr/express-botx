#!/usr/bin/env bash
#
# Примеры использования команды `express-botx api`.
#
# Использование:
#
#   PATH=./dist:$PATH \
#   OPTS="--config config-local.yaml --bot=devops-bot" \
#   CHAT=express-botx-development \
#     examples/scripts/api-command.sh
#
# Переменные:
#   OPTS — флаги подключения (конфиг, бот, хост и т.д.)
#   CHAT — алиас чата из конфига или UUID
#
set -euo pipefail
set -x

# ── Резолвим алиас чата в UUID ──────────────────────────────────────────────

CHAT_UUID=$(express-botx chats info $OPTS --chat-id="$CHAT" --format json \
  | grep -o '"group_chat_id": *"[^"]*"' | cut -d'"' -f4)

# ── Получить список чатов бота ───────────────────────────────────────────────

express-botx api $OPTS /api/v3/botx/chats/list

# ── Фильтрация ответа через jq (-q) ─────────────────────────────────────────

express-botx api $OPTS /api/v3/botx/chats/list -q '.result[].name'

# ── Показать заголовки ответа (-i) ───────────────────────────────────────────

express-botx api $OPTS -i /api/v3/botx/chats/list

# ── Типизированные поля: -f (строки) и -F (bool, number) ────────────────────

express-botx api $OPTS -X POST /api/v3/botx/chats/stealth_set \
  -f group_chat_id="$CHAT_UUID" \
  -F burn_in=60 \
  -F stealth=true

# ── Отправить уведомление в чат (--input файл, raw JSON) ────────────────────

PAYLOAD=$(mktemp)
cat > "$PAYLOAD" <<JSON
{
  "group_chat_id": "$CHAT_UUID",
  "notification": {
    "status": "ok",
    "body": "test from api command ($(date +%H:%M:%S))"
  }
}
JSON

express-botx api $OPTS -X POST /api/v4/botx/notifications/direct \
  --input "$PAYLOAD" -H 'Content-Type: application/json'

rm -f "$PAYLOAD"

# ── Создать чат (закомментировано — создаёт реальный чат, удалить через API нельзя)
#
# echo '{"name":"test","chat_type":"group_chat","members":["<USER-HUID>"]}' \
#   | express-botx api $OPTS -X POST /api/v3/botx/chats/create \
#       --input - -H 'Content-Type: application/json'
