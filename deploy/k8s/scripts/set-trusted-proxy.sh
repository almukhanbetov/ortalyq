#!/usr/bin/env bash
# Доверенный прокси бэкенда: только от этого адреса принимается X-Forwarded-For (адрес клиента для
# ограничений входа, регистрации и пробного урока). Доверяется ровно один адрес — тот, с которого Nginx
# хоста приходит в под через ClusterIP. Он зависит от CNI и режима kube-proxy, поэтому определяется на узле:
# скрипт сам отправляет с хоста к ClusterIP проверочные запросы с одноразовой меткой (заголовок
# X-Ortalyq-Proxy-Probe), а бэкенд пишет в журнал строку «proxy probe probe=<метка> peer=<адрес> trusted=…».
# Берётся только строка со своей меткой; старые строки журнала не используются.
# Результат — ConfigMap ortalyq-proxy (TRUSTED_PROXIES) и перезапуск backend. Прежнее значение хранится
# в аннотации ConfigMap, его возвращает --restore. Повторный запуск без изменений ничего не перезапускает.
# Требует образ backend с поддержкой проверочного запроса (пробный урок и новее). Запускает администратор на VPS.
#
#   deploy/k8s/scripts/set-trusted-proxy.sh             # определить адрес, применить и проверить
#   deploy/k8s/scripts/set-trusted-proxy.sh 10.244.0.1  # задать один адрес вручную (затем та же проверка)
#   deploy/k8s/scripts/set-trusted-proxy.sh --check     # только проверить текущее состояние, ничего не меняя
#   deploy/k8s/scripts/set-trusted-proxy.sh --restore   # вернуть значение, бывшее до последнего изменения
#   deploy/k8s/scripts/set-trusted-proxy.sh --remove    # удалить настройку (доверие только loopback)
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
need kubectl
need curl
need od

CM=ortalyq-proxy
ANN=ortalyq.compserv.cloud/previous-trusted-proxies
TEST_CLIENT=192.0.2.1   # адрес из документационного диапазона TEST-NET-1: «клиент» в X-Forwarded-For

be=$(kubectl -n "$NS" get svc backend -o jsonpath='{.spec.clusterIP}')
[[ $be =~ ^[0-9.]+$ ]] || die "нет ClusterIP сервиса backend"

# Один адрес IPv4 или IPv6 без маски; сети не принимаются.
valid_ip() {
  local a=$1 o
  if [[ $a =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]]; then
    for o in "${BASH_REMATCH[@]:1}"; do ((10#$o <= 255)) || return 1; done
    return 0
  fi
  [[ $a == *:* && $a =~ ^[0-9a-fA-F:]+$ ]]
}
# Адрес Nginx в под — внутренний адрес узла (сеть подов, CNI, loopback) или публичный IP самого узла.
plausible() {
  local a=$1
  [[ $a == "$PUBLIC_IP" ]] && return 0
  if [[ $a =~ ^([0-9]+)\.([0-9]+)\. ]]; then
    local x=${BASH_REMATCH[1]} y=${BASH_REMATCH[2]}
    ((x == 10 || x == 127)) && return 0
    ((x == 172 && y >= 16 && y <= 31)) && return 0
    ((x == 192 && y == 168)) && return 0
    ((x == 100 && y >= 64 && y <= 127)) && return 0
    return 1
  fi
  [[ ${a,,} =~ ^(f[cd]|fe80:|::1$) ]]
}

current() { kubectl -n "$NS" get configmap "$CM" -o jsonpath='{.data.TRUSTED_PROXIES}' 2>/dev/null || true; }
previous() { kubectl -n "$NS" get configmap "$CM" -o "jsonpath={.metadata.annotations.${ANN//./\\.}}" 2>/dev/null || true; }

# probe → «peer trusted forwarded_used» для собственного запроса с новой меткой
probe() {
  local mark line
  mark=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  curl -fsS --max-time 5 -o /dev/null -H "X-Forwarded-For: $TEST_CLIENT" -H "X-Ortalyq-Proxy-Probe: $mark" "http://$be:18080/api/health" ||
    die "проверочный запрос с хоста к backend $be:18080 не прошёл"
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    line=$(kubectl -n "$NS" logs deployment/backend --since=5m 2>/dev/null | grep -F "probe=$mark" || true)
    [[ -n $line ]] && break
    sleep 1
  done
  [[ -n $line ]] || die "бэкенд не записал проверочный запрос (образ backend старше этого скрипта? ограничение 20 проверок в минуту?)"
  [[ $(grep -c . <<<"$line") == 1 ]] || die "несколько строк с одной меткой — результат неоднозначен"
  local peer trusted used
  peer=$(sed -n 's/.* peer=\([0-9a-fA-F.:]*\).*/\1/p' <<<"$line")
  trusted=$(sed -n 's/.* trusted=\(true\|false\).*/\1/p' <<<"$line")
  used=$(sed -n 's/.* forwarded_used=\(true\|false\).*/\1/p' <<<"$line")
  [[ -n $peer && -n $trusted && -n $used ]] || die "не удалось разобрать строку журнала: $line"
  echo "$peer $trusted $used"
}

restart() {
  kubectl -n "$NS" rollout restart deployment/backend
  kubectl -n "$NS" rollout status deployment/backend --timeout=300s
}

# write_cm <значение|""> <прежнее> — "" удаляет ConfigMap
write_cm() {
  local val=$1 prev=$2
  if [[ -z $val ]]; then
    kubectl -n "$NS" delete configmap "$CM" --ignore-not-found
    return
  fi
  kubectl apply -f - >/dev/null <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CM
  namespace: $NS
  labels: {app.kubernetes.io/part-of: ortalyq}
  annotations: {$ANN: "${prev:-none}"}
data:
  TRUSTED_PROXIES: "$val"
YAML
}

# verify <адрес|""> — после перезапуска: собственный запрос через доверенный адрес должен быть принят
verify() {
  local want=$1 out peer trusted used
  out=$(probe) || exit 1
  read -r peer trusted used <<<"$out"
  info "Проверочный запрос: адрес соединения $peer, доверенный: $trusted, адрес из X-Forwarded-For принят: $used"
  if [[ -z $want ]]; then
    [[ $trusted == false && $used == false ]]
  else
    [[ $peer == "$want" && $trusted == true && $used == true ]]
  fi
}

case ${1:-} in
  --check)
    cur=$(current)
    info "ConfigMap $CM: TRUSTED_PROXIES=${cur:-<не задан — доверие только loopback>}"
    if verify "$cur"; then
      if [[ -n $cur ]]; then
        info "OK: X-Forwarded-For принимается только от $cur"
      else
        warn "TRUSTED_PROXIES не задан: подмена невозможна, но все клиенты за Nginx делят лимиты одного адреса"
      fi
      exit 0
    fi
    die "состояние не совпадает с ожидаемым — запустите скрипт без аргументов"
    ;;
  --remove | --restore)
    cur=$(current)
    if [[ $1 == --remove ]]; then
      target=""
    else
      [[ -n $cur ]] || die "ConfigMap $CM нет — возвращать нечего"
      target=$(previous)
      [[ -n $target ]] || die "в ConfigMap нет прежнего значения"
      [[ $target == none ]] && target=""
    fi
    [[ -z $target ]] || valid_ip "$target" || die "прежнее значение «$target» — не один адрес"
    if [[ $cur == "$target" ]]; then
      info "TRUSTED_PROXIES уже ${cur:-<не задан>} — изменений нет"
      exit 0
    fi
    info "TRUSTED_PROXIES: ${cur:-<не задан>} → ${target:-<не задан>}"
    write_cm "$target" "$cur"
    restart
    verify "$target" || die "проверка после изменения не прошла — см. kubectl -n $NS logs deploy/backend"
    [[ -n $target ]] || warn "TRUSTED_PROXIES не задан: подмена невозможна, но все клиенты за Nginx делят лимиты одного адреса"
    info "Готово"
    exit 0
    ;;
  -*) die "неизвестный параметр $1" ;;
esac

if [[ -n ${1:-} ]]; then
  addr=$1
  valid_ip "$addr" || die "нужен один IP-адрес без маски (сети не принимаются): $addr"
else
  # Три собственных запроса должны прийти с одного адреса; иначе путь Nginx → под неоднозначен.
  peers=()
  for _ in 1 2 3; do
    out=$(probe) || exit 1
    peers+=("${out%% *}")
  done
  mapfile -t uniq < <(printf '%s\n' "${peers[@]}" | sort -u)
  [[ ${#uniq[@]} == 1 ]] || die "запросы пришли с разных адресов (${uniq[*]}) — путь неоднозначен, задайте адрес вручную после проверки"
  addr=${uniq[0]}
  pod=$(kubectl -n "$NS" get endpoints backend -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)
  src=""
  [[ -n $pod ]] && src=$(ip -o route get "$pod" 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' || true)
  info "Адрес соединения Nginx-пути (хост → ClusterIP → под): $addr; маршрут хоста к поду $pod: src ${src:-?}"
fi
valid_ip "$addr" || die "получен не IP-адрес: $addr"
plausible "$addr" || die "адрес $addr не похож на адрес узла (не внутренний и не $PUBLIC_IP) — не доверяю; проверьте вручную"

cur=$(current)
oldprev=$(previous)
if [[ $cur == "$addr" ]]; then
  if verify "$addr"; then
    info "Уже настроено: TRUSTED_PROXIES=$addr — изменений нет, перезапуск не нужен"
    exit 0
  fi
  info "ConfigMap уже содержит $addr, но бэкенд его не применил — перезапуск"
  restart
else
  info "TRUSTED_PROXIES: ${cur:-<не задан>} → $addr (прежнее значение сохранено, возврат: $0 --restore)"
  write_cm "$addr" "$cur"
  restart
fi

if ! verify "$addr"; then
  warn "проверка не прошла — возвращаю прежнее значение (${cur:-<не задан>})"
  write_cm "$cur" "$oldprev" # аннотация «прежнее значение» остаётся как до запуска
  restart
  die "бэкенд не принял адрес $addr как доверенный; прежняя настройка восстановлена"
fi
info "Готово: X-Forwarded-For принимается только от $addr (Nginx хоста). Проверка в любой момент: $0 --check"
