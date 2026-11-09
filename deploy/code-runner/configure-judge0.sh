#!/usr/bin/env bash
# Приводит официальный judge0.conf (из релиза Judge0 CE v1.13.1) к безопасным настройкам ORTALYQ.
# Меняет только перечисленные ниже ключи (остальное — как в официальном файле); пустые пароли
# и ключ доступа генерирует. Значения секретов на экран не выводятся.
#
#   ./configure-judge0.sh judge0.conf
#
# Названия и смысл ключей — judge0.conf и https://ce.judge0.com (v1.13.1). Единицы: секунды, КБ.
set -euo pipefail
conf=${1:?укажите путь к judge0.conf из релиза Judge0 v1.13.1}
[[ -f $conf ]] || { echo "нет файла $conf" >&2; exit 1; }
command -v openssl >/dev/null || { echo "нужен openssl" >&2; exit 1; }

set_key() { # set_key KEY VALUE — заменить строку KEY=… или добавить в конец
  local k=$1 v=$2 tmp
  tmp=$(mktemp)
  awk -v k="$k" -v v="$v" 'BEGIN{done=0} $0 ~ "^"k"=" {print k"="v; done=1; next} {print} END{if(!done) print k"="v}' "$conf" > "$tmp"
  cat "$tmp" > "$conf"; rm -f "$tmp"
}
get_key() { sed -n "s/^$1=//p" "$conf" | tail -1; }
secret_if_empty() { [[ -n $(get_key "$1") ]] || set_key "$1" "$(openssl rand -hex "$2")"; }

chmod 600 "$conf"

# --- Доступ к API ---
set_key AUTHN_HEADER X-Auth-Token
secret_if_empty AUTHN_TOKEN 32       # ключ бэкенда ORTALYQ (CODE_RUNNER_TOKEN)
set_key AUTHZ_TOKEN ""               # пусто = административные вызовы (список/удаление отправок) запрещены
secret_if_empty REDIS_PASSWORD 24    # не оставлять значения по умолчанию (см. GHSA-q7vg-26pg-v5hr)
secret_if_empty POSTGRES_PASSWORD 24

# --- Режимы API ---
set_key ENABLE_WAIT_RESULT false          # бэкенд опрашивает результат сам
set_key ENABLE_BATCHED_SUBMISSIONS false
set_key ENABLE_SUBMISSION_DELETE false
set_key MAX_QUEUE_SIZE 50

# --- Сеть для программ запрещена (без --share-net isolate даёт отдельный netns только с loopback) ---
set_key ENABLE_NETWORK false
set_key ALLOW_ENABLE_NETWORK false        # запрос с enable_network=true будет отклонён

# --- Лимиты по умолчанию и максимумы (бэкенд передаёт значения в каждом запросе) ---
set_key CPU_TIME_LIMIT 2
set_key MAX_CPU_TIME_LIMIT 10
set_key CPU_EXTRA_TIME 0.5
set_key MAX_CPU_EXTRA_TIME 1
set_key WALL_TIME_LIMIT 5
set_key MAX_WALL_TIME_LIMIT 20
set_key MEMORY_LIMIT 131072               # КБ (128 МБ)
set_key MAX_MEMORY_LIMIT 524288           # КБ (512 МБ)
set_key STACK_LIMIT 64000
set_key MAX_STACK_LIMIT 64000
set_key MAX_PROCESSES_AND_OR_THREADS 16
set_key MAX_MAX_PROCESSES_AND_OR_THREADS 32
# Лимиты времени и памяти — на всю cgroup программы (сумма процессов), а не на процесс:
set_key ENABLE_PER_PROCESS_AND_THREAD_TIME_LIMIT false
set_key ALLOW_ENABLE_PER_PROCESS_AND_THREAD_TIME_LIMIT false
set_key ENABLE_PER_PROCESS_AND_THREAD_MEMORY_LIMIT false
set_key ALLOW_ENABLE_PER_PROCESS_AND_THREAD_MEMORY_LIMIT false
set_key MAX_FILE_SIZE 64                  # КБ на файл, в т.ч. stdout/stderr → SIGXFSZ (статус 8)
set_key MAX_MAX_FILE_SIZE 1024
set_key NUMBER_OF_RUNS 1
set_key MAX_NUMBER_OF_RUNS 1
set_key REDIRECT_STDERR_TO_STDOUT false

echo "Готово: $conf (права 600)."
echo "Ключ для бэкенда ORTALYQ (CODE_RUNNER_TOKEN) — значение AUTHN_TOKEN в этом файле; не пересылайте его открытым текстом."
