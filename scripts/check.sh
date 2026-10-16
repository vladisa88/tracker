#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
compose_test() { docker compose -f compose.test.yaml -f compose.backup-test.yaml "$@"; }
compose_test up -d --wait test-db restore-db storage
trap 'compose_test down -v' EXIT
compose_test build backup-test
# Retry only readiness: the test emulator may still be binding its socket.
attempt=0
until compose_test run --rm --no-deps --entrypoint aws backup-test --endpoint-url http://storage:9000 s3api create-bucket --bucket tracking-test; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 10 ] || exit 1
  sleep 1
done
compose_test run --rm --no-deps -v "$PWD/ops/backup/test_backup.py:/test_backup.py:ro" --entrypoint python3 backup-test /test_backup.py
export TEST_DATABASE_URL=postgres://tracking:test-only@127.0.0.1:55432/tracking_test?sslmode=disable
export TEST_BACKUP_RESTORE=1
(cd frontend && npm ci --ignore-scripts && npm run typecheck && npm run build && npx playwright install chromium)
(cd backend && go vet ./... && go test -race ./... -count=1)
(cd frontend && npm run test:e2e && npx playwright test -c playwright.bot.config.js)
