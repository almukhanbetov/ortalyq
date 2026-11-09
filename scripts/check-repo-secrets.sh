#!/usr/bin/env bash
# Проверяет файлы, отслеживаемые Git, на секреты. Значения найденных секретов не выводятся — только пути.
#
# 1. По имени: .env (кроме .env.example), приватные ключи и хранилища ключей, SSH-ключи,
#    файлы kubeconfig (kubeconfig, *.kubeconfig, kubeconfig.yaml, .kube/config), docker config, .netrc,
#    credentials.json. Скрипты и документация с «kubeconfig» в имени (*.sh, *.md, …) — не kubeconfig.
# 2. По содержимому (любое имя): заголовки приватных ключей, токены GitHub/AWS/Slack,
#    kubeconfig с литеральными учётными данными (token/client-key-data/password). Значения-подстановки
#    ($VAR, $(…), ${…}) учётными данными не считаются — так шаблоны в скриптах не дают ложных срабатываний.
#
# Использование: scripts/check-repo-secrets.sh            # файлы из git ls-files
#                git ls-files | scripts/check-repo-secrets.sh -   # список путей из stdin
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

if [[ ${1:-} == - ]]; then mapfile -t files; else mapfile -t files < <(git ls-files); fi

# --- 1. Имена файлов ---------------------------------------------------------
name_bad='(^|/)\.env(\..+)?$'                                     # .env, .env.local, .env.production
name_bad+='|\.(pem|key|p12|pfx|jks|keystore|ppk|asc|gpg)$'        # ключи и хранилища ключей
name_bad+='|(^|/)id_(rsa|dsa|ecdsa|ed25519)$'                     # приватные SSH-ключи (без .pub)
name_bad+='|(^|/)kubeconfig(\.ya?ml)?$|\.kubeconfig(\.ya?ml)?$|(^|/)\.kube/config$'
name_bad+='|(^|/)\.docker/config\.json$|(^|/)\.netrc$|(^|/)credentials\.json$'
name_ok='(^|/)\.env\.example$'

# --- 2. Содержимое ------------------------------------------------------------
content_bad='-----BEGIN ((RSA|EC|DSA|OPENSSH|ENCRYPTED|PGP) )?PRIVATE KEY( BLOCK)?-----'
content_bad+='|ghp_[A-Za-z0-9]{36}|gh[ousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{60,}'
content_bad+='|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}'
# kubeconfig: признак файла + литеральное значение учётных данных (не $…)
kube_marker='^[[:space:]]*(kind:[[:space:]]*Config|current-context:|clusters:)'
kube_cred='^[[:space:]]*-?[[:space:]]*(token|client-key-data|client-certificate-data|password):[[:space:]]*["'"'"']?[A-Za-z0-9+/=._-]{16,}'

bad=()
for f in "${files[@]}"; do
  [[ -f $f ]] || continue
  if [[ $f =~ $name_bad && ! $f =~ $name_ok ]]; then bad+=("$f (имя файла)"); continue; fi
  if grep -qIE -e "$content_bad" -- "$f" 2>/dev/null; then bad+=("$f (содержимое: ключ/токен)"); continue; fi
  if grep -qIE -e "$kube_marker" -- "$f" 2>/dev/null && grep -qIE -e "$kube_cred" -- "$f" 2>/dev/null; then
    bad+=("$f (содержимое: kubeconfig с учётными данными)")
  fi
done

if ((${#bad[@]})); then
  echo "В репозитории есть файлы, похожие на секреты:" >&2
  printf '  %s\n' "${bad[@]}" >&2
  exit 1
fi
echo "OK: проверено файлов: ${#files[@]}, секретов не найдено"
