# Рядом — учёт регулярных расходов

Реализован первый этап: вход одного владельца через Telegram, сохраняемые сессии и адаптивная оболочка. Подписки и напоминания появятся в следующих задачах.

## Локальный запуск

Требуются Docker Engine и Compose v2 с поддержкой `--wait`.

1. `cp .env.example .env`
2. В `.env` задайте случайный URL-safe пароль PostgreSQL, числовой Telegram ID владельца, Client ID и Client Secret из BotFather → Login Widget. Не используйте username или bot token вместо Client Secret.
3. В BotFather зарегистрируйте origin `http://localhost:8080` и callback `http://localhost:8080/api/auth/telegram/callback`. Если Telegram требует HTTPS для выбранного окружения, используйте HTTPS-домен с прокси и соответствующий `APP_ORIGIN`.
4. `docker compose up --build -d`
5. Откройте `http://localhost:8080`. API и собранный React обслуживаются одним Go-процессом с одного origin.

`docker compose down` сохраняет базу в именованном volume. `down -v` удаляет данные. База не публикует порт в рабочем Compose. Приложение слушает только loopback хоста; HTTPS-прокси должен вести на `127.0.0.1:8080`. В HTTPS-окружении задайте HTTPS `APP_ORIGIN`: cookie автоматически получает Secure. Адрес не выводится из недоверенных forwarded-заголовков. Реальные секреты и домен нужны только для живого входа; автоматические тесты обходятся без них.

Конфигурация проверяется до начала обслуживания; STATIC_DIR должен содержать читаемый index.html собранного фронтенда. Origin не содержит путь или завершающий `/`. HTTP допускается только для localhost/127.0.0.1. `DATABASE_URL` задаётся Compose; для самостоятельного запуска нужен PostgreSQL DSN. `STATIC_DIR` по умолчанию `frontend/dist` относительно рабочего каталога, в образе `/web`. Случайные идентификаторы сессий не требуют отдельного ключа подписи: в PostgreSQL сохраняется SHA-256 от 256-битного токена. Сессия действует семь дней, запрос входа — десять минут. Старые истёкшие записи удаляются при начале нового входа.

Миграции применяются транзакционно при старте под advisory lock. Более новая версия схемы блокирует запуск. `/health/live` проверяет процесс, `/health/ready` — доступность базы и версию схемы; ответы не раскрывают конфигурацию. Фоновый процесс пока не запускается: доставка относится к задачам 05–07.

## Проверки

На машине с Docker, Go 1.20+ и Node 22.12+ (или Node 24):

```sh
./scripts/check.sh
```

Скрипт поднимает отдельную настоящую PostgreSQL на loopback-порту 55432, выполняет проверку JavaScript через TypeScript `checkJs`, сборку, `go vet`, HTTP-тесты с race detector и браузерные сценарии Chromium для телефона и компьютера, затем удаляет тестовую базу. Требуется доступ к registry Go/npm и загрузке браузера. Не задавайте рабочую базу как `TEST_DATABASE_URL`.

Для разработки и отдельных проверок:

```sh
docker compose -f compose.test.yaml up -d --wait
export TEST_DATABASE_URL='postgres://tracking:test-only@127.0.0.1:55432/tracking_test?sslmode=disable'
(cd backend && go test ./internal/app -run TestOwnerLoginSessionAndLogout -v)
(cd backend && go vet ./...)
(cd frontend && npm install --ignore-scripts && npm run typecheck && npm run build)
(cd frontend && npx playwright install chromium && npm run test:e2e)
```

Без `TEST_DATABASE_URL` интеграционные тесты явно пропускаются; такой прогон не подтверждает вход и сохранение сессий. HTTP-тесты заменяют только транспорт внешнего Telegram и часы, используют настоящие RSA-подписи, JWKS, PKCE и PostgreSQL. Они проверяют чужой ID, повреждённую подпись, обязательные claims, issuer/audience/nonce, срок действия, повтор callback, CSRF, сохранение сессии при новом подключении и выход. Браузерный провайдер находится исключительно в `_test.go`; он не включается в рабочий бинарный файл. В production нет переменной или маршрута обхода авторизации.

Для будущих API-маршрутов добавляйте обработчики внутрь защищённой диспетчеризации `Server.ServeHTTP`. `GET /api/session` возвращает `owner.telegramId` (строка) и `csrfToken`. Изменяющие запросы должны отправлять заголовок `X-CSRF-Token`; Origin должен совпадать с `APP_ORIGIN`. Выход: `POST /api/logout`, ответ 204. Cookie — HttpOnly, SameSite=Lax, с ограниченным сроком; ответы API не кешируются. Telegram настроен на RS256 по умолчанию: другие алгоритмы намеренно отклоняются. Client Secret, access token и ID token не возвращаются клиенту и не журналируются.

## Git в ограниченной среде

Среда реализации запрещает запись в `.git`, поэтому метаданные размещены в `.git-storage` в этой же папке. Команды выполняются с `GIT_DIR=.git-storage GIT_WORK_TREE=.`. В обычной среде можно перенести `.git-storage` в `.git`, после чего Git работает стандартно. Каталог метаданных исключён из Docker context и индекса.
