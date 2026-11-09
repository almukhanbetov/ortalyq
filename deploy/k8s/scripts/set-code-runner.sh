#!/usr/bin/env bash
# Подключение бэкенда ORTALYQ к ОТДЕЛЬНОМУ серверу выполнения кода (Judge0, docs/CODE_RUNNER.md).
# Создаёт/обновляет Secret ortalyq-code-runner (адрес, ключ доступа, language_id Python)
# и перезапускает backend. Запускает администратор кластера (не CI).
#
#   deploy/k8s/scripts/set-code-runner.sh           # ввести адрес и ключ
#   deploy/k8s/scripts/set-code-runner.sh --remove  # отключить запуск кода (редактор продолжит работать)
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
need kubectl

if [[ ${1:-} == --remove ]]; then
  kubectl -n "$NS" delete secret ortalyq-code-runner --ignore-not-found
  kubectl -n "$NS" rollout restart deployment/backend
  kubectl -n "$NS" rollout status deployment/backend --timeout=300s
  info "Исполнитель отключён: запуск кода показывает «Сервис выполнения не настроен»"
  exit 0
fi

read -r -p "Адрес исполнителя (https://…, без / в конце): " url
url=${url%/}
[[ $url =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?$ ]] || die "нужен адрес вида https://runner.example.org (только HTTPS, без пути)"
read -r -s -p "Ключ доступа (AUTHN_TOKEN исполнителя, ввод скрыт): " token; echo
[[ ${#token} -ge 32 ]] || die "ключ короче 32 символов"
read -r -p "language_id Python 3 [71]: " pyid; pyid=${pyid:-71}
[[ $pyid =~ ^[0-9]+$ ]] || die "language_id — число"

# Проверка доступности до изменения кластера (ключ передаётся в заголовке через stdin-конфиг curl, не в argv)
if command -v curl >/dev/null; then
  code=$(printf 'header = "X-Auth-Token: %s"\n' "$token" | curl -sS -o /dev/null -w '%{http_code}' --max-time 10 -K - "$url/languages/$pyid" || true)
  [[ $code == 200 ]] || warn "GET $url/languages/$pyid вернул ${code:-нет ответа} — проверьте адрес, ключ, firewall исполнителя"
fi

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
chmod 700 "$tmp"
printf 'CODE_RUNNER_URL=%s\nCODE_RUNNER_TOKEN=%s\nCODE_RUNNER_PYTHON_ID=%s\n' "$url" "$token" "$pyid" > "$tmp/env"
kubectl -n "$NS" create secret generic ortalyq-code-runner --from-env-file="$tmp/env" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
info "Secret ortalyq-code-runner сохранён; перезапуск backend"
kubectl -n "$NS" rollout restart deployment/backend
kubectl -n "$NS" rollout status deployment/backend --timeout=300s
info "Готово. Проверка: в классе вкладка «Код» → «Запустить» (или deploy/code-runner/check-runner.sh)"
