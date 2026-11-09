#!/usr/bin/env bash
# Применяет манифесты namespace ortalyq (инфраструктура + приложение) — выполняет администратор.
# Образы backend/frontend не меняются: подставляются те, что уже работают в кластере.
# При первом применении (Deployment ещё нет) задайте IMAGE_SHA — полный SHA коммита с опубликованными образами.
#   IMAGE_SHA=<sha> deploy/k8s/scripts/apply.sh        # первый раз
#   deploy/k8s/scripts/apply.sh                         # изменения манифестов
#   deploy/k8s/scripts/apply.sh --yes                   # без вопроса
set -euo pipefail
. "$(dirname "$0")/lib.sh"
need kubectl
yes=${1:-}

for s in ortalyq-secrets ghcr-pull; do
  kubectl -n "$NS" get secret "$s" >/dev/null 2>&1 || die "нет Secret $s — сначала scripts/create-secrets.sh"
done
for pv in ortalyq-postgres ortalyq-s3; do
  kubectl get pv "$pv" >/dev/null 2>&1 || die "нет PV $pv — сначала scripts/setup-storage.sh"
done

img() { kubectl -n "$NS" get deploy "$1" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true; }
b=$(img backend); f=$(img frontend)
if [[ -z $b || -z $f ]]; then
  [[ ${IMAGE_SHA:-} =~ ^[0-9a-f]{40}$ ]] || die "первое применение: задайте IMAGE_SHA=<полный SHA коммита main с опубликованными образами>"
  b=${b:-$REGISTRY/ortalyq-backend:$IMAGE_SHA}; f=${f:-$REGISTRY/ortalyq-frontend:$IMAGE_SHA}
fi
info "backend:  $b"
info "frontend: $f"

out=$(mktemp); trap 'rm -f "$out"' EXIT
kubectl kustomize "$K8S_DIR/base" \
  | sed -e "s#$REGISTRY/ortalyq-backend:bootstrap#$b#" -e "s#$REGISTRY/ortalyq-frontend:bootstrap#$f#" > "$out"
grep -q ':bootstrap' "$out" && die "в манифестах остался тег bootstrap"

info "Изменения (kubectl diff):"
kubectl diff -f "$out" || true
if [[ $yes != --yes ]]; then
  read -r -p "Применить? [y/N] " a; [[ $a == y || $a == Y ]] || { echo "Отменено"; exit 0; }
fi
kubectl apply -f "$out"

info "Ожидание готовности"
kubectl -n "$NS" rollout status statefulset/postgres --timeout=300s
for d in redis rustfs livekit egress backend frontend; do
  kubectl -n "$NS" rollout status "deployment/$d" --timeout=300s
done
kubectl -n "$NS" get pods -o wide
