# RFC-010: Helm-чарт для деплоя в Kubernetes

- **Статус:** Draft
- **Дата:** 2026-03-14

## Контекст

`express-botx serve` — HTTP-сервер, который проксирует сообщения в eXpress через BotX API. Сейчас его можно запустить в Docker, но для деплоя в Kubernetes нужен Helm-чарт.

Сервер:
- Слушает на `:8080` (настраивается)
- Healthcheck: `GET /healthz` → `{"ok":true}`
- Требует конфиг с секретами (bot secret, API keys)
- Graceful shutdown по SIGTERM (10s timeout)
- Stateless (кэш токена — файл внутри пода)

Конфиг приложения поддерживает несколько ботов и несколько чатов:

```yaml
bots:
  prod:
    host: express.example.com
    id: <uuid>
    secret: <secret>
  staging:
    host: express-staging.example.com
    id: <uuid>
    secret: <secret>
chats:
  deploy: <chat-uuid>
  alerts: <chat-uuid>
  monitoring: <chat-uuid>
```

## Предложение

Helm-чарт в директории `charts/express-botx/`. Публикация как OCI-артефакт в `ghcr.io`, индексация на ArtifactHub.

### Структура

```
charts/express-botx/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── secret.yaml
│   ├── hpa.yaml
│   ├── serviceaccount.yaml
│   ├── ingress.yaml
│   └── NOTES.txt
artifacthub-repo.yml          # в корне репо
.github/workflows/chart.yml   # CI для сборки и пуша
```

### Chart.yaml

```yaml
apiVersion: v2
name: express-botx
description: eXpress BotX API gateway
type: application
version: 0.1.0
appVersion: "0.1.0"
home: https://github.com/lavr/express-botx
sources:
  - https://github.com/lavr/express-botx
maintainers:
  - name: lavr
```

### values.yaml

```yaml
replicaCount: 1

image:
  repository: lavr/express-botx
  tag: ""  # defaults to appVersion
  pullPolicy: IfNotPresent

# Конфиг express-botx — монтируется из Secret в /etc/express-botx/config.yaml
# Содержит секреты (bot secret, API keys), поэтому Secret, а не ConfigMap
config:
  bots:
    prod:
      host: express.example.com
      id: "bot-uuid-here"
      secret: "bot-secret-here"
    # staging:
    #   host: express-staging.example.com
    #   id: "staging-bot-uuid"
    #   secret: "staging-bot-secret"
  chats:
    deploy: "chat-uuid-here"
    # alerts: "alerts-chat-uuid"
    # monitoring: "monitoring-chat-uuid"
  cache:
    type: file
    ttl: 3600
  server:
    listen: ":8080"
    base_path: /api/v1
    allow_bot_secret_auth: false
    api_keys: []
    #  - name: alertmanager
    #    key: "change-me"
    # alertmanager:
    #   chat_id: alerts
    # grafana:
    #   chat_id: monitoring

# Использовать существующий Secret вместо генерации из config.*
# Secret должен содержать ключ "config.yaml" с полным YAML-конфигом
existingSecret: ""

service:
  type: ClusterIP
  port: 80
  targetPort: 8080

ingress:
  enabled: false
  className: ""
  annotations: {}
  hosts:
    - host: express-botx.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    memory: 128Mi

autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 5
  targetCPUUtilizationPercentage: 80

serviceAccount:
  create: true
  name: ""
  annotations: {}

podAnnotations: {}
podLabels: {}
nodeSelector: {}
tolerations: []
affinity: {}

extraEnv: []
```

### Секреты

Весь конфиг содержит секреты, поэтому монтируется из **Secret**. Два варианта:

1. **Генерация из values** (по умолчанию) — чарт рендерит `config.*` в YAML и кладёт в Secret.
2. **Внешний Secret** (`existingSecret`) — для External Secrets Operator, Sealed Secrets и т.п. Secret должен содержать ключ `config.yaml`.

### Secret (шаблон)

```yaml
{{- if not .Values.existingSecret }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "express-botx.fullname" . }}
  labels: {{- include "express-botx.labels" . | nindent 4 }}
type: Opaque
stringData:
  config.yaml: |
    bots:
      {{- range $name, $bot := .Values.config.bots }}
      {{ $name }}:
        host: {{ $bot.host | quote }}
        id: {{ $bot.id | quote }}
        secret: {{ $bot.secret | quote }}
      {{- end }}
    {{- if .Values.config.chats }}
    chats:
      {{- range $alias, $uuid := .Values.config.chats }}
      {{ $alias }}: {{ $uuid | quote }}
      {{- end }}
    {{- end }}
    cache:
      {{- toYaml .Values.config.cache | nindent 6 }}
    server:
      {{- toYaml .Values.config.server | nindent 6 }}
{{- end }}
```

### Deployment (ключевые части)

```yaml
volumes:
  - name: config
    secret:
      secretName: {{ .Values.existingSecret | default (include "express-botx.fullname" .) }}
      items:
        - key: config.yaml
          path: config.yaml
  - name: cache
    emptyDir: {}
containers:
  - name: express-botx
    command: ["express-botx"]
    args: ["serve", "--config", "/etc/express-botx/config.yaml"]
    ports:
      - name: http
        containerPort: 8080
    volumeMounts:
      - name: config
        mountPath: /etc/express-botx
        readOnly: true
      - name: cache
        mountPath: /tmp/express-botx
    livenessProbe:
      httpGet:
        path: /healthz
        port: http
      initialDelaySeconds: 5
      periodSeconds: 10
    readinessProbe:
      httpGet:
        path: /healthz
        port: http
      initialDelaySeconds: 2
      periodSeconds: 5
    securityContext:
      runAsNonRoot: true
      runAsUser: 65534
      readOnlyRootFilesystem: true
      allowPrivilegeEscalation: false
```

## Публикация

### OCI Registry (ghcr.io)

Чарт публикуется как OCI-артефакт:

```
oci://ghcr.io/lavr/charts/express-botx
```

Установка:
```bash
helm install express-botx oci://ghcr.io/lavr/charts/express-botx --version 0.1.0
```

### GitHub Actions: `.github/workflows/chart.yml`

Триггеры:
- Push тега `chart-*` (например `chart-0.1.0`) — сборка и пуш в ghcr.io
- Изменения в `charts/` на main — только lint

```yaml
name: Helm Chart

on:
  push:
    tags:
      - "chart-*"
    branches:
      - main
    paths:
      - "charts/**"
  pull_request:
    paths:
      - "charts/**"

permissions:
  contents: read
  packages: write

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      - name: Set up Helm
        uses: azure/setup-helm@v4

      - name: Lint chart
        run: helm lint charts/express-botx

  publish:
    if: startsWith(github.ref, 'refs/tags/chart-')
    needs: lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      - name: Set up Helm
        uses: azure/setup-helm@v4

      - name: Log in to ghcr.io
        run: echo "${{ secrets.GITHUB_TOKEN }}" | helm registry login ghcr.io -u ${{ github.actor }} --password-stdin

      - name: Package chart
        run: helm package charts/express-botx

      - name: Push to OCI registry
        run: helm push express-botx-*.tgz oci://ghcr.io/lavr/charts
```

### ArtifactHub

Файл `artifacthub-repo.yml` в корне репо:

```yaml
repositoryID: ""  # заполняется после регистрации на artifacthub.io
owners:
  - name: lavr
    email: <email>
```

Регистрация:
1. Зайти на https://artifacthub.io, авторизоваться через GitHub
2. Add repository → Type: **OCI**, URL: `oci://ghcr.io/lavr/charts/express-botx`
3. ArtifactHub автоматически подтягивает новые версии

## Использование

```bash
# Минимальный деплой (один бот, один чат)
helm install express-botx oci://ghcr.io/lavr/charts/express-botx \
  --set config.bots.prod.host=express.example.com \
  --set config.bots.prod.id=9e944012-... \
  --set config.bots.prod.secret=aa484a61... \
  --set config.chats.deploy=7ee8aaa9-...

# Несколько ботов и чатов из values-файла
helm install express-botx oci://ghcr.io/lavr/charts/express-botx -f my-values.yaml

# С Ingress
helm install express-botx oci://ghcr.io/lavr/charts/express-botx \
  -f my-values.yaml \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=bot.example.com

# С внешним Secret (External Secrets Operator и т.п.)
helm install express-botx oci://ghcr.io/lavr/charts/express-botx \
  --set existingSecret=my-express-botx-secret
```

## Версионирование

- Версия чарта (`Chart.yaml version`) и `appVersion` обновляются независимо
- Тег `chart-X.Y.Z` триггерит публикацию чарта
- Тег `X.Y.Z` (без префикса) триггерит релиз бинарника и Docker-образа (существующий workflow)

## Что НЕ включаем в P0

- **PodDisruptionBudget** — добавим при production-нагрузке
- **NetworkPolicy** — зависит от CNI
- **Vault Agent sidecar** — отдельный RFC
- **Prometheus ServiceMonitor** — нет /metrics эндпоинта

## Файлы

| Действие | Файл |
|----------|------|
| CREATE | `charts/express-botx/Chart.yaml` |
| CREATE | `charts/express-botx/values.yaml` |
| CREATE | `charts/express-botx/templates/_helpers.tpl` |
| CREATE | `charts/express-botx/templates/deployment.yaml` |
| CREATE | `charts/express-botx/templates/service.yaml` |
| CREATE | `charts/express-botx/templates/secret.yaml` |
| CREATE | `charts/express-botx/templates/hpa.yaml` |
| CREATE | `charts/express-botx/templates/serviceaccount.yaml` |
| CREATE | `charts/express-botx/templates/ingress.yaml` |
| CREATE | `charts/express-botx/templates/NOTES.txt` |
| CREATE | `charts/express-botx/.helmignore` |
| CREATE | `.github/workflows/chart.yml` |
| CREATE | `artifacthub-repo.yml` |

## Проверка

1. `helm lint charts/express-botx` — валидация
2. `helm template express-botx charts/express-botx --set config.bots.a.host=x --set config.bots.a.id=y --set config.bots.a.secret=z --set config.bots.b.host=x2 --set config.bots.b.id=y2 --set config.bots.b.secret=z2 --set config.chats.deploy=uuid1 --set config.chats.alerts=uuid2` — рендеринг с несколькими ботами и чатами
3. Проверить что Secret содержит корректный YAML со всеми ботами и чатами
4. `git tag chart-0.1.0 && git push --tags` — триггерит CI, пуш в ghcr.io
5. Проверить `helm pull oci://ghcr.io/lavr/charts/express-botx --version 0.1.0`
6. Деплой в тестовый кластер, `kubectl get pods`, проверить /healthz
