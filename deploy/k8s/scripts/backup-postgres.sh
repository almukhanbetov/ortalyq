#!/usr/bin/env bash
# Резервная копия PostgreSQL (pg_dump, custom format) в /srv/ortalyq/backups. Запуск под devops.
# Рекомендуется перед каждым развёртыванием с миграциями и по расписанию (cron).
set -euo pipefail
. "$(dirname "$0")/lib.sh"
dir=${BACKUP_DIR:-/srv/ortalyq/backups}
umask 077
f="$dir/ortalyq-$(date -u +%Y%m%dT%H%M%SZ).dump"
kubectl -n "$NS" exec statefulset/postgres -- pg_dump -U ortalyq -d ortalyq -Fc > "$f.part"
mv "$f.part" "$f"
ls -lh "$f"
# Хранить 14 последних копий
ls -1t "$dir"/ortalyq-*.dump 2>/dev/null | tail -n +15 | xargs -r rm -f
