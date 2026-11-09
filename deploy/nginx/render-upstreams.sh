#!/usr/bin/env bash
# Записывает ClusterIP сервисов frontend/backend в /etc/nginx/ortalyq/upstreams.conf.
# Nginx на хосте обращается к ClusterIP напрямую (kube-proxy на этом же узле) — без NodePort,
# hostPort и ingress-контроллера, и без открытия новых портов наружу.
# ClusterIP не меняется, пока Service не удалён; после пересоздания Service запустите скрипт снова.
# Запуск на VPS под devops: deploy/nginx/render-upstreams.sh
set -euo pipefail
ns=ortalyq
ip() { kubectl -n "$ns" get svc "$1" -o jsonpath='{.spec.clusterIP}'; }
fe=$(ip frontend); be=$(ip backend)
[[ $fe =~ ^[0-9.]+$ && $be =~ ^[0-9.]+$ ]] || { echo "Не удалось получить ClusterIP (frontend=$fe backend=$be)" >&2; exit 1; }

echo "Проверка доступа с хоста к ClusterIP:"
curl -fsS --max-time 5 "http://$be:18080/api/health" >/dev/null && echo "  backend  $be:18080 OK" || { echo "  backend $be:18080 недоступен с хоста" >&2; exit 1; }
curl -fsS --max-time 5 -o /dev/null "http://$fe:3000/login" && echo "  frontend $fe:3000 OK" || { echo "  frontend $fe:3000 недоступен с хоста" >&2; exit 1; }

tmp=$(mktemp)
cat > "$tmp" <<CONF
# Сгенерировано deploy/nginx/render-upstreams.sh $(date -u +%FT%TZ). Не редактировать вручную.
upstream ortalyq_frontend { server $fe:3000; keepalive 16; }
upstream ortalyq_backend  { server $be:18080; keepalive 16; }
CONF
sudo install -d -m 0755 /etc/nginx/ortalyq
sudo install -m 0644 "$tmp" /etc/nginx/ortalyq/upstreams.conf
rm -f "$tmp"
sudo nginx -t
echo "Готово. Примените: sudo systemctl reload nginx"
