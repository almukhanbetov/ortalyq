#!/usr/bin/env bash
# Только чтение: проверяет сервер перед первым развёртыванием. Ничего не меняет.
# Запуск на VPS под devops: deploy/k8s/scripts/preflight.sh
set -uo pipefail
. "$(dirname "$0")/lib.sh"
need kubectl
fail=0
check() { if eval "$2" >/dev/null 2>&1; then echo "  OK   $1"; else echo "  FAIL $1"; fail=1; fi; }

info "Kubernetes"
kubectl version 2>/dev/null | sed 's/^/  /'
nodes=$(kubectl get nodes -o name 2>/dev/null | wc -l)
[[ $nodes == 1 ]] && echo "  OK   один узел: $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')" || { echo "  FAIL узлов: $nodes (ожидался 1)"; fail=1; }
echo "  runtime: $(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.containerRuntimeVersion}')"
echo "  API: $(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
if kubectl get ns "$NS" >/dev/null 2>&1; then
  echo "  namespace $NS существует; ресурсы в нём:"
  kubectl -n "$NS" get all,pvc,secret -o name 2>/dev/null | sed 's/^/    /'
else
  echo "  namespace $NS ещё не создан"
fi
echo "  StorageClass: $(kubectl get sc -o name 2>/dev/null | tr '\n' ' ')"
echo "  PV ortalyq-*: $(kubectl get pv -o name 2>/dev/null | grep ortalyq | tr '\n' ' ')"
check "Docker Engine не требуется (используется containerd)" "true"

info "Порты хоста, которые займёт ORTALYQ (должны быть свободны)"
for p in 7880/tcp 7881/tcp 7882/udp 17891/tcp; do
  port=${p%/*}; proto=${p#*/}
  flag=$([[ $proto == udp ]] && echo -lnu || echo -lnt)
  if ss "$flag" 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]${port}$"; then echo "  FAIL $p занят"; fail=1; else echo "  OK   $p свободен"; fi
done
for p in 3001 8080 80 443; do
  ss -lnt | awk '{print $4}' | grep -Eq "[:.]${p}$" && echo "  info $p занят существующими сервисами — ORTALYQ его не использует"
done

info "DNS"
for h in "$SITE" "$LIVEKIT_HOST"; do
  ip=$(getent ahostsv4 "$h" | awk 'NR==1{print $1}')
  [[ $ip == "$PUBLIC_IP" ]] && echo "  OK   $h → $ip" || { echo "  FAIL $h → ${ip:-нет} (ожидался $PUBLIC_IP)"; fail=1; }
done

info "Nginx и сертификаты"
check "nginx установлен" "command -v nginx"
check "certbot установлен" "command -v certbot"
if sudo -n true 2>/dev/null; then
  sudo nginx -T 2>/dev/null | grep -nE "server_name[^;]*ortalyq" | sed 's/^/  уже есть: /' || true
  sudo test -d "/etc/letsencrypt/live/$SITE" && echo "  сертификат $SITE уже выпущен" || echo "  сертификат $SITE ещё не выпущен"
  sudo ufw status 2>/dev/null | sed 's/^/  ufw: /' | head -20 || true
else
  echo "  (нет sudo без пароля — проверки nginx -T/ufw пропущены; запустите вручную: sudo nginx -T | grep ortalyq)"
fi

info "Ресурсы"
echo "  CPU: $(nproc), RAM: $(free -h | awk '/Mem:/{print $2" всего, "$7" доступно"}')"
df -h /srv 2>/dev/null | sed 's/^/  /'
[[ -d /srv/ortalyq ]] && ls -la /srv/ortalyq | sed 's/^/  /'

info "SSH"
if sudo -n true 2>/dev/null; then sudo sshd -T 2>/dev/null | grep -E '^(port|allowusers|allowgroups|passwordauthentication) ' | sed 's/^/  /'; fi

echo
[[ $fail == 0 ]] && echo "Предварительная проверка пройдена." || echo "Есть проблемы (FAIL) — устраните до развёртывания."
exit $fail
