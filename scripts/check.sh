#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
docker compose -f compose.test.yaml up -d --wait
trap 'docker compose -f compose.test.yaml down -v' EXIT
export TEST_DATABASE_URL=postgres://tracking:test-only@127.0.0.1:55432/tracking_test?sslmode=disable
(cd frontend && npm ci --ignore-scripts && npm run typecheck && npm run build && npx playwright install chromium)
(cd backend && go vet ./... && go test -race ./... -count=1)
(cd frontend && npm run test:e2e && npx playwright test -c playwright.bot.config.js)
