#!/usr/bin/env bash
# Создаёт каталоги постоянных данных и local PV с привязкой к узлу (Retain).
# Существующие данные не удаляются и не перезаписываются. Повторный запуск безопасен.
# Запуск на VPS под devops (нужен sudo для /srv): deploy/k8s/scripts/setup-storage.sh
set -euo pipefail
. "$(dirname "$0")/lib.sh"
need kubectl

node=$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}')
[[ -n $node && $node != *" "* ]] || die "ожидался ровно один узел, получено: '$node'"
info "Узел: $node"

# postgres:17-alpine — uid/gid 70; rustfs — uid/gid 10001
sudo install -d -m 0755 -o root -g root /srv/ortalyq
sudo install -d -m 0700 -o 70 -g 70 /srv/ortalyq/postgres
sudo install -d -m 0750 -o 10001 -g 10001 /srv/ortalyq/s3
sudo install -d -m 0700 -o "$(id -u)" -g "$(id -g)" /srv/ortalyq/backups
ls -la /srv/ortalyq

for pv in ortalyq-postgres ortalyq-s3; do
  if kubectl get pv "$pv" >/dev/null 2>&1; then
    echo "PV $pv уже существует ($(kubectl get pv "$pv" -o jsonpath='{.status.phase}')) — не изменяю"
  fi
done
sed "s/__NODE_NAME__/$node/g" "$K8S_DIR/storage/persistent-volumes.yaml" | kubectl apply -f -
kubectl get pv ortalyq-postgres ortalyq-s3
