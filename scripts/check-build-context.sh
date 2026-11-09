#!/usr/bin/env bash
# Проверяет, что сборочные контексты Docker (backend/, frontend/) не содержат .env, ключей и других секретов.
# Контекст воспроизводится через BuildKit: в образ-«снимок» копируется ровно то, что Docker отправил бы
# при сборке (с учётом .dockerignore), затем список файлов проверяется по шаблонам.
# Использование: scripts/check-build-context.sh [backend frontend]
set -euo pipefail
cd "$(dirname "$0")/.."

bad_names='(^|/)(\.env(\..*)?|.*\.(pem|key|crt|p12|pfx|kubeconfig)|id_(rsa|ed25519|ecdsa)(\.pub)?|kubeconfig|credentials(\.json)?|.*\.(sql|dump|backup)(\.gz)?|.*\.(mp4|webm))$'
allowed='(^|/)\.env\.example$|/migrations/[^/]+\.sql$'
# Высокоэнтропийные токены известных форматов (значения не выводятся).
bad_content='AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|xox[bp]-[A-Za-z0-9-]{10,}'

status=0
for ctx in "${@:-backend frontend}"; do
  for c in $ctx; do
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    # Dockerfile-«снимок»: копирует весь контекст как есть (.dockerignore применяется Docker'ом).
    printf 'FROM scratch\nCOPY . /ctx/\n' > "$tmp/Dockerfile"
    docker buildx build --quiet -f "$tmp/Dockerfile" --output "type=local,dest=$tmp/out" "$c" >/dev/null
    files=$(cd "$tmp/out/ctx" && find . -type f | sed 's|^\./||' | sort)
    count=$(printf '%s\n' "$files" | grep -c . || true)
    names=$(printf '%s\n' "$files" | grep -Ei "$bad_names" | grep -Ev "$allowed" || true)
    content=$(cd "$tmp/out/ctx" && grep -rlIE "$bad_content" . 2>/dev/null | sed 's|^\./||' || true)
    if [[ -n "$names$content" ]]; then
      echo "ОШИБКА: в контексте $c найдены файлы, похожие на секреты:" >&2
      # shellcheck disable=SC2086 # список файлов — по одному на строку
      printf '  %s\n' $names $content >&2
      status=1
    else
      echo "OK: контекст $c — $count файлов, секретов не найдено"
    fi
    rm -rf "$tmp"; trap - EXIT
  done
done
exit $status
