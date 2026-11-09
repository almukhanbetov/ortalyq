#!/usr/bin/env bash
# Создаёт kubeconfig сервисного аккаунта ortalyq-deployer (только namespace ortalyq) и проверяет его права.
# Выполняет администратор после apply.sh. Токен не выводится.
#   deploy/k8s/scripts/make-deployer-kubeconfig.sh /tmp/deployer.kubeconfig
set -euo pipefail
. "$(dirname "$0")/lib.sh"
need kubectl
out=${1:?укажите путь к файлу kubeconfig}
umask 077

for _ in $(seq 1 30); do
  token=$(kubectl -n "$NS" get secret ortalyq-deployer-token -o jsonpath='{.data.token}' 2>/dev/null || true)
  [[ -n $token ]] && break; sleep 1
done
[[ -n ${token:-} ]] || die "токен ortalyq-deployer-token не создан (примените apply.sh)"
ca=$(kubectl -n "$NS" get secret ortalyq-deployer-token -o jsonpath='{.data.ca\.crt}')
server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')

cat > "$out" <<KCFG
apiVersion: v1
kind: Config
clusters:
  - name: ortalyq
    cluster:
      server: $server
      certificate-authority-data: $ca
users:
  - name: ortalyq-deployer
    user:
      token: $(printf '%s' "$token" | base64 -d)
contexts:
  - name: ortalyq-deployer
    context: {cluster: ortalyq, user: ortalyq-deployer, namespace: $NS}
current-context: ortalyq-deployer
KCFG
unset token

info "Проверка прав (ожидается yes/no как указано)"
ok=1
expect() {
  want=$1; shift
  got=$(kubectl --kubeconfig "$out" auth can-i "$@" 2>/dev/null || true)
  printf '  %-3s (ожидается %-3s) kubectl auth can-i %s\n' "$got" "$want" "$*"
  [[ $got == "$want" ]] || ok=0
}
expect yes patch deployment/backend -n "$NS"
expect yes create jobs -n "$NS"
expect yes get pods/log -n "$NS"
expect no  patch deployment/postgres -n "$NS"
expect no  get secrets -n "$NS"
expect no  create deployments -n "$NS"
expect no  get pods -n default
expect no  get pods -n kube-system
expect no  list nodes
[[ $ok == 1 ]] || die "права сервисного аккаунта не совпадают с ожидаемыми"
echo "kubeconfig записан в $out (права 600). Установите его скриптом install-deploy-user.sh."
