# express-botx

CLI и HTTP-сервер для отправки сообщений в корпоративный мессенджер [eXpress](https://express.ms) через BotX API.

Принимает вебхуки от Alertmanager и Grafana, поддерживает асинхронную отправку через RabbitMQ/Kafka, работает как утилита командной строки или HTTP-сервис.

## Возможности

- **Отправка сообщений** из CLI, скриптов, пайплайнов CI/CD
- **API-запросы** — произвольные вызовы eXpress BotX API из командной строки с автоаутентификацией
- **HTTP-сервер** с API для отправки и приёма вебхуков
- **Alertmanager и Grafana** — готовые эндпоинты для мониторинга
- **Асинхронная очередь** — RabbitMQ или Kafka для надёжной доставки
- **Секреты** — поддержка переменных окружения и HashiCorp Vault
- **Kubernetes-ready** — Docker, Helm chart, бинарник


## Quick Start

### Установка бинарной сборки

```bash
curl -sL "https://github.com/lavr/express-botx/releases/latest/download/express-botx-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/').tar.gz" | tar xz
sudo mv express-botx /usr/local/bin/
```

Проект также можно установить из homebrew, собрать из исходников, запустить в готовом контейнере.

Подробнее: [docs/install.md](docs/install.md)

### Создание конфига

В конфиге можно сохранить параметры бота и параметры чатов.

Добавить параметры бота в конфиг:
 
```bash
express-botx config bot add \
  --name mybot \
  --host express.company.ru \
  --bot-id 054af49e-5e18-4dca-ad73-4f96b6de63fa \
  --secret my-bot-secret
```

Добавить параметры чата в конфиг:
 
```bash
express-botx config chat add \
  --chat-id aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa \
  --alias alerts
```

Теперь можно отправить сообщение:

```bash
express-botx send "Привет из express-botx!"
```

Подробнее: [docs/commands.md](docs/commands.md)

### API-запросы

Отправить произвольный запрос к BotX API:

```bash
# Список чатов
express-botx api /api/v3/botx/chats/list

# POST с JSON-телом
express-botx api -X POST /api/v3/botx/chats/create -f name=test -f chat_type=group_chat

# Фильтрация ответа
express-botx api /api/v3/botx/chats/list -q '.result[].name'
```

Больше примеров: [examples/scripts/api-command.sh](examples/scripts/api-command.sh) | Подробнее: [docs/commands.md](docs/commands.md#api)

### HTTP-сервер (serve)

Если нужно запустить как веб-сервис:

```bash
# Создать токен для доступа к веб-сервису
NEWAPIKEY=$(openssl rand -hex 32)
express-botx config apikey add --name mykey1 --key "$NEWAPIKEY"

# Запустить в режиме serve
express-botx serve

# Отправить сообщение через веб-сервис
curl -X POST http://localhost:8080/api/v1/send \
    -H "Authorization: Bearer <api-key>" \
    -H "Content-Type: application/json" \
    -d '{"message": "Test from express-botx web api"}'
```


Эндпоинты (все POST требуют `Authorization: Bearer <key>`):

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/healthz` | Проверка здоровья |
| `POST` | `/api/v1/send` | Отправка сообщения |
| `POST` | `/api/v1/alertmanager` | Вебхук Alertmanager |
| `POST` | `/api/v1/grafana` | Вебхук Grafana |

Сервер автоматически добавляет заголовок `X-Request-ID` к каждому ответу (если клиент не передал свой, генерируется уникальный). Все HTTP-запросы логируются в stderr (метод, путь, статус, время выполнения).

Подробнее: [docs/integrations.md](docs/integrations.md)


### Очереди (enqueue / worker)

Для асинхронной доставки express-botx поддерживает работу через RabbitMQ или Kafka. HTTP-сервер кладёт сообщения в очередь, worker забирает и отправляет в BotX API.

```bash
# Producer: HTTP → очередь
express-botx serve --enqueue

# Consumer: очередь → BotX API
express-botx worker
```

Подробнее: [docs/async-queues.md](docs/async-queues.md)

### Управление конфигурацией

```bash
express-botx config bot add --name prod --host express.company.ru --bot-id UUID --secret SECRET
express-botx config chat add --chat-id UUID --alias deploy --bot prod
express-botx config apikey add --name app1
express-botx config show
```

Полный список команд: [docs/commands.md](docs/commands.md)

## Конфигурация

Приложение может работать без конфига - параметры бота и чата можно задать из командной строки.
Для удобной работы можно прописать в конфиг параметры бота/ботов и чатов, например:

```yaml
bots:
  prod:
    host: express.company.ru
    id: 054af49e-5e18-4dca-ad73-4f96b6de63fa
    token: eyJhbGci...

chats:
  alerts:
    id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
    bot: prod
    default: true
```

Полный референс конфигурации: [docs/configuration.md](docs/configuration.md)

## Интеграции

В режиме веб-сервера есть методы для интеграции с alertmanager и grafana.

Пример конфига alertmanager:

```yaml
# alertmanager.yml
receivers:
  - name: express
    webhook_configs:
      - url: http://express-botx:8080/api/v1/alertmanager
        send_resolved: true
        http_config:
          bearer_token: "<api-key>"
```

Подробнее: [docs/integrations.md](docs/integrations.md)

## Деплой

Приложение собирается в образ `lavr/express-botx`.

Его можно запустить:



```bash
# HTTP-сервер
docker run -d -p 8080:8080 -v ./config.yaml:/config.yaml \
  lavr/express-botx serve --config /config.yaml
```

Хелм-чарт для установки в kubernetes:

```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx -f values.yaml
```

Подробнее: [docs/deployment.md](docs/deployment.md)


## Документация

| Документ | Описание |
|----------|----------|
| [docs/install.md](docs/install.md) | Варианты установки |
| [docs/commands.md](docs/commands.md) | Все команды и флаги |
| [docs/configuration.md](docs/configuration.md) | Полный референс конфигурации |
| [docs/integrations.md](docs/integrations.md) | Alertmanager, Grafana, примеры |
| [docs/deployment.md](docs/deployment.md) | Docker, Helm, systemd, docker-compose |
| [docs/async-queues.md](docs/async-queues.md) | RabbitMQ, Kafka, архитектура очередей |
| [docs/quickstart.md](docs/quickstart.md) | Базовые сценарии настройки |

## Лицензия

MIT
