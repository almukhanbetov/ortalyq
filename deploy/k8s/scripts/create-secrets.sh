#!/usr/bin/env bash
# Создаёт в кластере Secret ortalyq-secrets со случайными значениями и Secret ghcr-pull для GHCR.
# Значения не выводятся и не сохраняются на диск. Существующие секреты не перезаписываются.
# Запуск на VPS под devops: deploy/k8s/scripts/create-secrets.sh
set -euo pipefail
. "$(dirname "$0")/lib.sh"
need kubectl; need openssl
umask 077

kubectl get ns "$NS" >/dev/null 2>&1 || kubectl apply -f "$K8S_DIR/base/namespace.yaml"

if kubectl -n "$NS" get secret ortalyq-secrets >/dev/null 2>&1; then
  echo "Secret ortalyq-secrets уже существует — оставляю без изменений."
  echo "(Смена POSTGRES_PASSWORD не меняет пароль в уже созданной БД — см. docs/DEPLOY.md «Ротация секретов».)"
else
  pg=$(openssl rand -hex 24)
  tmp=$(mktemp -d /dev/shm/ortalyq.XXXXXX 2>/dev/null || mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  cat > "$tmp/env" <<ENV
POSTGRES_PASSWORD=$pg
DATABASE_URL=postgres://ortalyq:$pg@postgres:5432/ortalyq?sslmode=disable
APP_SECRET=$(openssl rand -hex 32)
LIVEKIT_API_KEY=API$(openssl rand -hex 6)
LIVEKIT_API_SECRET=$(openssl rand -hex 32)
S3_ACCESS_KEY=$(openssl rand -hex 10)
S3_SECRET_KEY=$(openssl rand -hex 24)
ENV
  kubectl -n "$NS" create secret generic ortalyq-secrets --from-env-file="$tmp/env" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  rm -rf "$tmp"; trap - EXIT
  echo "Secret ortalyq-secrets создан (ключи: $(kubectl -n "$NS" get secret ortalyq-secrets -o go-template='{{range $k,$v := .data}}{{$k}} {{end}}'))"
fi

if kubectl -n "$NS" get secret ghcr-pull >/dev/null 2>&1; then
  echo "Secret ghcr-pull уже существует — оставляю без изменений."
else
  echo "Токен GitHub (classic PAT) только с правом read:packages для скачивания образов из GHCR."
  read -r -p "Логин GitHub [almukhanbetov]: " gh_user; gh_user=${gh_user:-almukhanbetov}
  read -r -s -p "Токен (ввод скрыт): " gh_token; echo
  [[ -n $gh_token ]] || die "токен не введён"
  auth=$(printf '%s:%s' "$gh_user" "$gh_token" | base64 -w0)
  cfg=$(printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$auth" | base64 -w0)
  unset gh_token auth
  printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: ghcr-pull\n  namespace: %s\ntype: kubernetes.io/dockerconfigjson\ndata:\n  .dockerconfigjson: %s\n' "$NS" "$cfg" | kubectl apply -f - >/dev/null
  unset cfg
  echo "Secret ghcr-pull создан."
fi
