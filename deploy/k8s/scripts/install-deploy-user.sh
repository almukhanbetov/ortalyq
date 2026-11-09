#!/usr/bin/env bash
# Создаёт системного пользователя ortalyq-deploy для GitHub Actions:
#  - вход только по ключу, ключ ограничен командой /usr/local/sbin/ortalyq-deploy (restrict: без shell,
#    без проброса портов/агента/pty);
#  - authorized_keys принадлежит root — пользователь не может его изменить;
#  - kubeconfig сервисного аккаунта ortalyq-deployer (не административный) в /etc/ortalyq.
# Запуск: sudo deploy/k8s/scripts/install-deploy-user.sh <github-deploy-key.pub> <deployer.kubeconfig>
set -euo pipefail
. "$(dirname "$0")/lib.sh"
[[ $EUID == 0 ]] || die "запустите через sudo"
pub=${1:?путь к публичному ключу}; kcfg=${2:?путь к kubeconfig из make-deployer-kubeconfig.sh}
[[ $(wc -l < "$pub") -le 1 ]] && grep -Eq '^(ssh-ed25519|ecdsa-sha2-nistp256) ' "$pub" || die "ожидается один публичный ключ ed25519/ecdsa"
command -v kubectl >/dev/null || die "kubectl не найден в PATH"
user=ortalyq-deploy home=/var/lib/ortalyq-deploy

id "$user" >/dev/null 2>&1 || useradd --system --home-dir "$home" --create-home --shell /bin/bash "$user"
passwd -l "$user" >/dev/null
install -d -m 0750 -o "$user" -g "$user" "$home"
install -d -m 0755 -o root -g root "$home/.ssh"
printf 'restrict,command="/usr/local/sbin/ortalyq-deploy" %s\n' "$(cat "$pub")" > "$home/.ssh/authorized_keys"
chown root:root "$home/.ssh/authorized_keys"; chmod 0644 "$home/.ssh/authorized_keys"

install -m 0755 -o root -g root "$(dirname "$0")/ortalyq-deploy" /usr/local/sbin/ortalyq-deploy
install -d -m 0750 -o root -g "$user" /etc/ortalyq
install -m 0640 -o root -g "$user" "$kcfg" /etc/ortalyq/deployer.kubeconfig

if sshd -T 2>/dev/null | grep -Eq '^allowusers '; then
  sshd -T | grep -E '^allowusers ' | grep -qw "$user" || warn "в sshd_config задан AllowUsers без $user — добавьте его, иначе вход будет отклонён"
fi
if sshd -T 2>/dev/null | grep -Eq '^allowgroups '; then warn "в sshd_config задан AllowGroups — проверьте, что $user входит в разрешённую группу"; fi

echo "Готово. Проверка от имени пользователя:"
sudo -u "$user" /usr/local/sbin/ortalyq-deploy status | head -20
