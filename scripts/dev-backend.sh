#!/usr/bin/env bash
# Запуск бэкенда вне Docker с переменными из .env (режим разработки).
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; source .env; set +a
cd backend
exec go run ./cmd/server "${@:-serve}"
