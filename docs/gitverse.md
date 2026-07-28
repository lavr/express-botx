# Зеркало на GitVerse

Репозиторий зеркалируется на [gitverse.ru/lavr/express-botx](https://gitverse.ru/lavr/express-botx).
Зеркало обновляется вручную (push в обычный репозиторий GitVerse; это не «mirror»-импорт —
для импортированных зеркал GitVerse отключает CI/CD).

## Синхронизация

Remote добавляется один раз:

```bash
git remote add gitverse git@gitverse.ru:lavr/express-botx.git
```

Обновление зеркала:

```bash
git push gitverse main --tags
```

Push тега вида `X.Y.Z` запускает на GitVerse релизный workflow (см. ниже) —
релиз с бинарниками публикуется в разделе
[Релизы](https://gitverse.ru/lavr/express-botx/releases) независимо от GitHub.

## CI на GitVerse

Workflow-файлы живут в [`.gitverse/workflows/`](../.gitverse/workflows/):

- `test.yml` — build, vet, `go test` + интеграционные тесты Vault (OpenBao);
  запускается на push веток и pull request;
- `release.yml` — по тегу `X.Y.Z`: тесты → создание релиза через API GitVerse →
  сборка бинарников (linux/darwin amd64+arm64, windows amd64), каждая
  build-джоба сама заливает свой архив ассетом в релиз.

Синтаксис совместим с GitHub Actions, actions резолвятся из зеркал
`gitverse.ru/sc/actions/*` (`actions/checkout@v5`, `actions/setup-go@v6` работают).

Отличия площадки от GitHub Actions:

- артефакты — только `actions/upload-artifact@v4` / `download-artifact@v4`,
  суммарный лимит 500 МБ на аккаунт, хранение до 30 дней;
- лимит времени одной задачи на облачном раннере — 15 минут;
- лимит сборочного времени — 1000 мин/мес для публичных репозиториев;
- docker недоступен (нет dind; для сборки образов рекомендован kaniko), поэтому
  e2e-джобы с service-контейнерами (RabbitMQ/Kafka) и сборка docker-образов
  остаются только на GitHub;
- в настройках репозитория (`settings#cicd`) выбирается, какую директорию
  использовать: `.gitverse/workflows` или `.github/workflows`. Выбрано
  `.gitverse` — GitHub-workflow на GitVerse не запускаются.

Подводные камни (проверено на практике, июль 2026):

- **reusable workflows не работают**: джоба с
  `uses: ./.gitverse/workflows/test.yml` навсегда зависает в «waiting»
  (не планируется на раннер), хотя фича задокументирована. Отменить зависший
  прогон можно только из UI (API cancel отвечает 400). Поэтому тестовые джобы
  продублированы в `release.yml` инлайном;
- **`upload-artifact@v4` флейкает**: зеркало `gitverse.ru/sc/actions/upload-artifact`
  периодически отдаёт апстримную сборку, падающую с «GHES not supported»;
  зависит от кеша экшенов на конкретном раннере. В `release.yml` артефакты
  не используются — ассеты заливаются в релиз напрямую из build-джоб;
- **rate limit api.github.com**: безтокенные запросы к GitHub API с общих IP
  облачных раннеров часто упираются в лимит — версия OpenBao при недоступном
  API берётся из пинового fallback;
- job outputs (`$GITHUB_OUTPUT` + `needs.<job>.outputs`) работают;
  `GITHUB_REF_NAME`, `github.ref` — тоже; `github.server_url` на GitVerse
  равен `https://gitverse.ru/sc`;
- смена настройки CI/CD (директории workflow) не перезапускает прогоны для
  уже запушенных коммитов — нужен новый push.

## Секреты

Релизному workflow нужен секрет `RELEASE_TOKEN` (настройки репозитория →
секреты CI/CD): personal access token с правом «Публичное API»
(создаётся в [настройках профиля](https://gitverse.ru/settings/tokens),
показывается один раз).

Имя секрета не может начинаться с `GITVERSE_` — этот префикс зарезервирован
платформой.

## Чтение статусов и логов CI из терминала

Официального CLI у GitVerse нет, но публичный API отдаёт прогоны и логи.
Обёртка: [`scripts/gitverse-ci.sh`](../scripts/gitverse-ci.sh):

```bash
export GITVERSE_TOKEN=...   # или положить токен в ~/.gitverse_token

scripts/gitverse-ci.sh runs            # список прогонов
scripts/gitverse-ci.sh last            # джобы последнего прогона
scripts/gitverse-ci.sh jobs <run_id>   # джобы прогона
scripts/gitverse-ci.sh logs <job_id>   # полный лог джобы
```

## Публичный API GitVerse (шпаргалка)

База: `https://api.gitverse.ru`. Обязательные заголовки:

```
Authorization: Bearer <token>
Accept: application/vnd.gitverse.object+json;version=1
```

Проверенные endpoints (июль 2026):

| Метод | Путь | Примечание |
| --- | --- | --- |
| GET | `/repos/{o}/{r}` | информация о репозитории |
| GET | `/repos/{o}/{r}/actions/runs` | прогоны CI (недокументирован) |
| GET | `/repos/{o}/{r}/actions/runs/{id}/jobs` | джобы прогона |
| GET | `/repos/{o}/{r}/actions/jobs/{id}/logs` | лог джобы, plain text |
| GET | `/repos/{o}/{r}/releases` | список релизов (404, если релизов нет) |
| POST | `/repos/{o}/{r}/releases` | создать релиз: JSON `{tag_name, name, body}` |
| POST | `/repos/{o}/{r}/releases/{id}/assets?name=<имя>` | залить ассет: multipart `attachment=@файл` |
| DELETE | `/repos/{o}/{r}/releases/{id}` | удалить релиз |

Ассеты релизов скачиваются публично, без токена.
