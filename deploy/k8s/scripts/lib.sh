# shellcheck shell=bash disable=SC2034
# Общие функции скриптов развёртывания ORTALYQ.
NS=ortalyq
REGISTRY=ghcr.io/almukhanbetov
SITE=ortalyq.compserv.cloud
LIVEKIT_HOST=livekit.ortalyq.compserv.cloud
PUBLIC_IP=194.31.55.106
K8S_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die()  { echo "ОШИБКА: $*" >&2; exit 1; }
info() { echo "==> $*"; }
warn() { echo "ВНИМАНИЕ: $*" >&2; }
need() { command -v "$1" >/dev/null 2>&1 || die "не найдена команда $1"; }
