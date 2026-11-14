# Развёртывание ORTALYQ: CI/CD и Kubernetes

Сайт: **https://ortalyq.compserv.cloud**. LiveKit: **wss://livekit.ortalyq.compserv.cloud**.
VPS 194.31.55.106 (SSH-порт 22122). На нём один узел Kubernetes с containerd и Nginx на 80/443.

> Документ описывает **первичную настройку**. Пока по этой инструкции на сервере ничего не применялось.

## 1. Как это устроено

```
GitHub Actions
  CI       (PR и main)     go vet/test + PostgreSQL, tsc, next build, проверка контекста — без секретов
  Images   (push в main)   ghcr.io/almukhanbetov/ortalyq-{backend,frontend}:<полный SHA>  (тег не перезаписывается)
  Deploy   (только вручную) ── SSH :22122 ──► ortalyq-deploy@VPS
                                              └─ принудительная команда /usr/local/sbin/ortalyq-deploy
                                                 └─ kubeconfig SA ortalyq-deployer (Role в namespace ortalyq)
                                                    1) Job миграций  2) set image backend → frontend  3) rollout status / undo
VPS
  Nginx :443 ─ ortalyq.compserv.cloud ─┬─ /api/* (+WebSocket) ─► ClusterIP backend:18080
                                       └─ /                     ─► ClusterIP frontend:3000
             ─ livekit.ortalyq…       ───────────────────────────► 127.0.0.1:7880 (LiveKit, сеть хоста)
  Медиа WebRTC напрямую: 194.31.55.106 UDP 7882 и TCP 7881
  namespace ortalyq: postgres (StatefulSet), rustfs (S3), redis, backend, frontend, livekit, egress
  Данные: /srv/ortalyq/postgres и /srv/ortalyq/s3 — local PV, Retain, привязка к узлу
```

### Почему так

- **Без ingress-контроллера.** Порты 80/443 заняты Nginx хоста. Nginx обращается к ClusterIP сервисов напрямую: kube-proxy работает на этом же узле. Новые порты наружу не открываются, а 3001/8080 старого приложения не используются.
- **LiveKit и Egress работают в сети хоста** (`hostNetwork`), как в docker-compose. WebRTC-медиа не проходит через NAT kube-proxy, а Egress (headless Chrome) подключается к LiveKit по `ws://127.0.0.1:7880`. Сервисы кластера (redis, frontend, rustfs, backend) они находят через `dnsPolicy: ClusterFirstWithHostNet`.
  - Сигнальный порт 7880 привязан к 127.0.0.1 и доступен снаружи только через Nginx (TLS). Это проверено на образе livekit v1.13.11: `bind_addresses` ограничивает только HTTP-порт, медиапорты 7881/7882 слушают все интерфейсы.
  - Встроенный TURN выключен: его relay-диапазон (30000–40000) пересёкся бы с NodePort, а TURN/TLS на 443 занят Nginx. Запасной путь для медиа — ICE/TCP 7881.
- **Подключение CI/CD к Kubernetes — через SSH с принудительной командой.**
  - API-сервер Kubernetes в интернет не открывается.
  - GitHub хранит только SSH-ключ. Этот ключ не даёт shell: `restrict,command=…`, а `authorized_keys` принадлежит root.
  - Ключ позволяет выполнить ровно три действия: `deploy <sha> <digest> <digest>`, `status`, `rollback`. Аргументы строго проверяются регулярными выражениями.
  - Сам скрипт работает с kubeconfig сервисного аккаунта `ortalyq-deployer`, а не с административным. Права этого аккаунта:
    - `patch` только для Deployment `backend` и `frontend`;
    - чтение deployments, replicasets, pods, logs, events;
    - создание и удаление Job (для миграций).
  - Доступа к Secrets, ConfigMaps, PVC, RBAC и другим namespace нет.
  - Альтернатива — выставить API-сервер наружу и хранить токен SA в GitHub. Её отвергли: она открывает API кластера в интернет, а утёкший токен работает откуда угодно.
- **Образы по digest.** Deploy получает digest опубликованного образа и выкатывает `…:<sha>@sha256:…`, поэтому даже перезапись тега в GHCR не подменит выкатываемый образ.
- **Миграции отдельно от выката.** Перед сменой образов запускается Job `server migrate` на новом образе.
  - Если Job падает, приложение не трогается.
  - Каждая миграция выполняется в своей транзакции под advisory lock.
  - **Откат образа не откатывает схему.** Миграции должны быть только расширяющими (см. §8).
- **Одно развёртывание за раз:** `concurrency: deploy-production` в GitHub плюс `flock` на сервере.
- **Таймауты:**
  - rollout — 300 с; `progressDeadlineSeconds` — 300;
  - Job миграций — 600 с;
  - весь workflow — 25 мин.
  - Если rollout не уложился в таймаут, выполняется автоматический `rollout undo`.
- **Backend — одна реплика:** realtime-хаб доски и чата живёт в памяти процесса. Не масштабируйте backend больше 1.

## 2. Файлы

| Путь | Назначение |
|---|---|
| `.github/workflows/ci.yml` | Тесты backend (юнит и интеграция с PostgreSQL), smoke-запуск сервера, `tsc`, `next build`, проверка контекста |
| `.github/workflows/images.yml` | PR: сборка без публикации. main: публикация в GHCR с тегом SHA, без перезаписи |
| `.github/workflows/deploy.yml` | Ручной запуск: deploy / status / rollback через SSH |
| `scripts/check-build-context.sh` | Проверяет фактический контекст Docker на .env, ключи, дампы и видео |
| `deploy/k8s/base/*` | Kustomize: namespace, RBAC, postgres, rustfs, redis, backend, frontend, livekit, egress, ConfigMap |
| `deploy/k8s/storage/persistent-volumes.yaml` | local PV (Retain, nodeAffinity, claimRef) |
| `deploy/k8s/scripts/preflight.sh` | Проверка сервера, только чтение |
| `deploy/k8s/scripts/setup-storage.sh` | Каталоги `/srv/ortalyq/*` и PV |
| `deploy/k8s/scripts/create-secrets.sh` | Secret `ortalyq-secrets` (случайные значения) и `ghcr-pull` |
| `deploy/k8s/scripts/apply.sh` | Применение манифестов администратором (образы не меняет) |
| `deploy/k8s/scripts/make-deployer-kubeconfig.sh` | kubeconfig SA и проверка его прав |
| `deploy/k8s/scripts/install-deploy-user.sh` | Пользователь `ortalyq-deploy`, принудительная команда, kubeconfig |
| `deploy/k8s/scripts/ortalyq-deploy` | Принудительная команда: миграции, выкат, откат |
| `deploy/k8s/scripts/backup-postgres.sh` | `pg_dump` в `/srv/ortalyq/backups` |
| `deploy/nginx/*.conf`, `render-upstreams.sh` | Nginx только для двух новых поддоменов |
| `deploy/k8s/scripts/set-code-runner.sh` | Подключение к отдельному серверу выполнения кода (Secret `ortalyq-code-runner`) |
| `deploy/k8s/scripts/set-trusted-proxy.sh` | Адрес Nginx хоста как единственного доверенного прокси (ConfigMap `ortalyq-proxy`, `TRUSTED_PROXIES`) |
| `deploy/code-runner/*` | Сервер выполнения кода (Judge0 + Caddy) — **отдельная VM**, не этот VPS; см. [CODE_RUNNER.md](CODE_RUNNER.md) |

### Секреты (только в кластере, не в Git)

Secret `ortalyq-secrets` создаёт `create-secrets.sh`. Значения генерируются на сервере и нигде не выводятся. Ключи:
- `POSTGRES_PASSWORD`, `DATABASE_URL`;
- `APP_SECRET`;
- `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`;
- `S3_ACCESS_KEY`, `S3_SECRET_KEY`.

Secret `ghcr-pull` — токен для скачивания образов из GHCR.

Secret `ortalyq-code-runner` (необязательный) — адрес и ключ исполнителя кода, создаёт `set-code-runner.sh`. Без него редактор кода работает, запуск недоступен.

Secret `ortalyq-openai` (необязательный) — `OPENAI_API_KEY` и `OPENAI_MODEL` для ИИ «Учебной мастерской» (docs/WORKSHOP.md). Без него мастерская работает без ИИ. Создание (ключ вводится скрыто и не попадает ни в историю оболочки, ни в аргументы процессов):

```bash
read -rsp "OPENAI_API_KEY: " k; echo; read -rp "OPENAI_MODEL: " m
f=$(mktemp) && chmod 600 "$f" && printf 'OPENAI_API_KEY=%s\nOPENAI_MODEL=%s\n' "$k" "$m" > "$f" && unset k
kubectl -n ortalyq create secret generic ortalyq-openai --from-env-file="$f" --dry-run=client -o yaml | kubectl apply -f -
rm -f "$f"
kubectl -n ortalyq rollout restart deployment/backend
```

Миграция `0004_code_editor` только добавляет таблицы `code_docs` и `code_runs`. Предыдущий образ backend на новой схеме работает (проверено), поэтому откат образа после неё безопасен.

Миграции `0006_diagnostics` (таблица `diagnostic_reports`) и `0007_diagnostic_review` (новые столбцы со значениями по умолчанию) только расширяют схему. Предыдущий образ backend на новой схеме работает (проверено), откат образа безопасен. Версия релиза (SHA коммита) передаётся в оба образа при сборке (`APP_VERSION` в `images.yml`) и видна в диагностике; проверить образ backend: `kubectl -n ortalyq exec deploy/backend -- server version`.

## 3. GitHub: подготовка

1. **Settings → Actions → General.**
   - Workflow permissions: *Read repository contents* (workflow сами запрашивают `packages: write` только при публикации).
   - Fork pull request workflows: *Require approval for all outside collaborators*.
2. **Settings → Environments → New environment `production`.**
   - Deployment branches: *Selected branches* → `main`.
   - Required reviewers: себя (рекомендуется, тогда каждый деплой нужно подтвердить).
   - **Environment secrets:**

   | Имя | Значение |
   |---|---|
   | `DEPLOY_HOST` | `194.31.55.106` |
   | `DEPLOY_PORT` | `22122` |
   | `DEPLOY_USER` | `ortalyq-deploy` |
   | `DEPLOY_SSH_KEY` | приватный ключ из шага 5.1 (весь файл) |
   | `DEPLOY_KNOWN_HOSTS` | строки из шага 5.2 |

   > Environments в **приватных** репозиториях доступны на GitHub Pro/Team/Enterprise. На бесплатном плане
   > создайте эти же пять секретов как *Repository secrets* (Settings → Secrets and variables → Actions)
   > и удалите блок `environment:` из `deploy.yml`. Deploy запускается только вручную, а CI/Images эти секреты не используют.

   Секреты лежат в environment, а не в репозитории. Workflow из PR их не получают: в PR не запускается Deploy, а CI и Images не обращаются к environment.
3. **Запустите сборку образов.** Сделайте push в `main` (или Actions → Images → Run workflow). Появятся приватные пакеты `ortalyq-backend` и `ortalyq-frontend` (Your profile → Packages), они связаны с репозиторием через label `org.opencontainers.image.source`.
4. **Создайте токен для скачивания образов на VPS.** GitHub → Settings → Developer settings → Personal access tokens → **Tokens (classic)** → scope только **`read:packages`**, срок действия — по вашей политике. Fine-grained токены GHCR не поддерживают. Токен понадобится в шаге 4.4, нигде его не сохраняйте.

## 4. VPS: первичная настройка (под devops)

```bash
ssh -p 22122 devops@194.31.55.106
cd /home/devops/ortalyq && git pull --ff-only origin main
```

1. **Проверка (только чтение):**
   ```bash
   deploy/k8s/scripts/preflight.sh
   ```
   Порты 7880/tcp, 7881/tcp, 7882/udp и 17891/tcp должны быть свободны, DNS обоих поддоменов → 194.31.55.106. Скрипт также показывает версию nginx, наличие certbot, AllowUsers в sshd, CPU, RAM и диск.

2. **Firewall** — откройте только медиапорты LiveKit. Пример для ufw; если используется другой firewall, сделайте аналогично:
   ```bash
   sudo ufw allow 7881/tcp comment 'ortalyq livekit ice/tcp'
   sudo ufw allow 7882/udp comment 'ortalyq livekit media'
   ```
   **Не открывайте:**
   - 7880 (LiveKit API, и так на 127.0.0.1);
   - 17891 (health Egress);
   - 5432, 6379, 9000 и NodePort-диапазон.

   PostgreSQL, Redis и S3 работают как ClusterIP и наружу не публикуются. Веб-консоль RustFS выключена.

3. **Хранилище:**
   ```bash
   deploy/k8s/scripts/setup-storage.sh
   ```
   - Создаёт `/srv/ortalyq/postgres` (uid 70, 0700), `/srv/ortalyq/s3` (uid 10001) и `/srv/ortalyq/backups`.
   - Создаёт PV `ortalyq-postgres` (20Gi) и `ortalyq-s3` (100Gi) с привязкой к узлу и Retain.
   - Существующие данные не трогает.

   Ёмкость в PV номинальная: local-том занимает столько, сколько есть на диске. Следите за `df -h /srv`.

4. **Секреты:**
   ```bash
   deploy/k8s/scripts/create-secrets.sh
   ```
   Скрипт спросит логин GitHub и classic PAT с правом `read:packages`; ввод скрыт.

5. **Первое применение манифестов.** Возьмите полный SHA коммита `main`, для которого workflow **Images** завершился успешно:
   ```bash
   IMAGE_SHA=<полный SHA> deploy/k8s/scripts/apply.sh
   ```
   - Скрипт показывает `kubectl diff`, спрашивает подтверждение и ждёт готовности всех компонентов.
   - Схему БД при первом старте создаёт backend (`serve` применяет миграции).
   - Если что-то не запустилось — `kubectl -n ortalyq get pods`, `kubectl -n ortalyq describe pod …`, `kubectl -n ortalyq logs …`.

6. **Сертификаты и Nginx** (только новые файлы; конфигурацию compserv.cloud не меняем):
   ```bash
   sudo install -d -m 0755 /var/www/ortalyq-acme
   sudo cp deploy/nginx/ortalyq-http.conf /etc/nginx/sites-available/
   sudo ln -s /etc/nginx/sites-available/ortalyq-http.conf /etc/nginx/sites-enabled/
   sudo nginx -t && sudo systemctl reload nginx

   sudo certbot certonly --webroot -w /var/www/ortalyq-acme \
     --cert-name ortalyq.compserv.cloud \
     -d ortalyq.compserv.cloud -d livekit.ortalyq.compserv.cloud \
     --deploy-hook 'systemctl reload nginx'

   deploy/nginx/render-upstreams.sh          # ClusterIP → /etc/nginx/ortalyq/upstreams.conf (+ проверка доступа с хоста)
   sudo cp deploy/nginx/ortalyq.compserv.cloud.conf deploy/nginx/livekit.ortalyq.compserv.cloud.conf /etc/nginx/sites-available/
   sudo ln -s /etc/nginx/sites-available/ortalyq.compserv.cloud.conf /etc/nginx/sites-enabled/
   sudo ln -s /etc/nginx/sites-available/livekit.ortalyq.compserv.cloud.conf /etc/nginx/sites-enabled/
   sudo nginx -t && sudo systemctl reload nginx
   ```
   - Если в системе используется `/etc/nginx/conf.d/*.conf` вместо sites-enabled, положите файлы туда.
   - Сертификат выпускается отдельный (`--cert-name ortalyq.compserv.cloud`), существующие сертификаты не меняются. Продление делает уже установленный таймер certbot; после продления выполняется reload nginx.
   - Конфигурации проверены `nginx -t` на nginx 1.24 и 1.28. Директива `http2 on` не используется из-за 1.24.
   - Если `render-upstreams.sh` сообщает, что ClusterIP недоступен с хоста (бывает с некоторыми CNI и режимами kube-proxy), не продолжайте — пришлите вывод, подберём другой способ.

   **Доверенный прокси (обязательно).** Бэкенд принимает адрес клиента из `X-Forwarded-For` только от адресов из `TRUSTED_PROXIES`; от остальных берёт адрес соединения. По этому адресу считаются ограничения входа, регистрации и пробного урока. Nginx хоста приходит в под с адреса, который зависит от CNI и режима kube-proxy, поэтому он определяется на узле:
   ```bash
   deploy/k8s/scripts/set-trusted-proxy.sh          # собственные проверочные запросы → один адрес → ConfigMap ortalyq-proxy → перезапуск backend → проверка
   deploy/k8s/scripts/set-trusted-proxy.sh --check  # проверить в любой момент; --restore — вернуть прежнее значение; --remove — удалить
   ```
   - Нужен образ backend с поддержкой проверочного запроса (с выпуска «Пробный урок»). Запускайте после выката образов.
   - Без этого шага подменить адрес нельзя, но все посетители выглядят одним адресом Nginx и делят лимиты (например, 30 попыток входа за 10 минут на всех). В журнале бэкенда тогда есть `forwarded client address ignored ... peer=<адрес>`.
   - Доверяется ровно один адрес. Скрипт останавливается при неоднозначном результате и сам возвращает прежнее значение, если проверка после перезапуска не прошла. Адрес можно задать вручную: `set-trusted-proxy.sh 10.244.0.1` (только IP без маски). Бэкенд не запустится с `0.0.0.0/0`, `*` и сетями шире /8.
   - Повторите после пересоздания кластерной сети или смены CNI. Nginx менять не нужно: `X-Real-IP $remote_addr` и `X-Forwarded-For $proxy_add_x_forwarded_for` уже передают настоящий адрес клиента крайним справа, а подставленное клиентом слева бэкенд пропускает.

7. **Проверка:**
   ```bash
   curl -fsS https://ortalyq.compserv.cloud/api/ready
   curl -fsS https://livekit.ortalyq.compserv.cloud/      # → OK
   ```
   Затем откройте сайт и проверьте видеосвязь и запись (§6).

## 5. Доступ GitHub Actions к VPS

1. **На своём компьютере** создайте отдельный ключ, только для деплоя:
   ```bash
   ssh-keygen -t ed25519 -N '' -C github-actions-ortalyq -f ./ortalyq-gha
   ```
   Содержимое `ortalyq-gha` (приватный ключ) → секрет `DEPLOY_SSH_KEY`. Файл `ortalyq-gha.pub` скопируйте на VPS. После этого удалите приватный ключ с компьютера: он нужен только GitHub.
2. **Отпечаток хоста** (защита от подмены сервера). Получите и сверьте:
   ```bash
   ssh-keyscan -p 22122 194.31.55.106 2>/dev/null          # → DEPLOY_KNOWN_HOSTS
   ssh -p 22122 devops@194.31.55.106 'for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done'
   ```
   Отпечатки из второй команды должны совпасть с `ssh-keygen -lf <(ssh-keyscan -p 22122 194.31.55.106)`.
3. **На VPS:**
   ```bash
   deploy/k8s/scripts/make-deployer-kubeconfig.sh /tmp/deployer.kubeconfig   # проверит права SA: yes/no
   sudo deploy/k8s/scripts/install-deploy-user.sh ~/ortalyq-gha.pub /tmp/deployer.kubeconfig
   shred -u /tmp/deployer.kubeconfig
   ```
   - Если в `sshd_config` задан `AllowUsers` или `AllowGroups`, скрипт предупредит. Тогда добавьте `ortalyq-deploy` и выполните `sudo sshd -t && sudo systemctl reload ssh`.
   - Вход по паролю для этого пользователя заблокирован.
4. **Проверка из GitHub:** Actions → **Deploy** → Run workflow → action `status`.

## 6. Выкат

Actions → **Deploy** → Run workflow:
- `sha` — пусто (HEAD `main`) или полный SHA коммита из `main`;
- `action` — `deploy`.

Что происходит:
1. Workflow проверяет, что коммит входит в `main` и образы этого SHA опубликованы, и получает их digest.
2. Скрипт на сервере ждёт блокировку и запускает Job миграций.
3. Затем выкатывает backend, после него frontend.
4. При неудаче — `rollout undo`.
5. Workflow проверяет `https://ortalyq.compserv.cloud/api/ready` снаружи.

Автоматическое развёртывание **не включено**. Чтобы включить его позже, добавьте в `deploy.yml` триггер `workflow_run` по завершении Images.

**Перед выкатом с новыми миграциями** сделайте резервную копию: `deploy/k8s/scripts/backup-postgres.sh`.

### Выпуск пробного урока (первый выкат с ним)

Что меняется: миграция `0008_demo.sql` (новые таблицы `demo_rooms`, `demo_participants` — только добавление), ConfigMap `ortalyq-config` (`DEMO_*`), в Deployment backend — переменная `TRUSTED_PROXIES` из необязательного ConfigMap `ortalyq-proxy`. Сервисы, PV, Nginx и LiveKit не меняются, поэтому `render-upstreams.sh` не нужен (только если Service пересоздавали).

1. Дождитесь зелёных **CI** и **Images** для SHA выпуска. Выберите время без идущих занятий.
2. На VPS под devops:
   ```bash
   cd /home/devops/ortalyq && git pull --ff-only origin main && git log -1 --format=%H   # = SHA выпуска
   deploy/k8s/scripts/backup-postgres.sh      # перед миграцией
   deploy/k8s/scripts/apply.sh                # ConfigMap DEMO_* и env TRUSTED_PROXIES; образы сохраняются
   ```
   ConfigMap получает новое имя (хэш), поэтому `apply.sh` перезапустит **backend и LiveKit** (backend — со старым образом, новые переменные он не читает). Идущие видеозвонки прервутся — выполняйте вне занятий. Frontend не перезапускается.
3. Actions → **Deploy** → `sha` = SHA выпуска, `action` = `deploy` (Job миграций → backend → frontend → проверка `/api/ready`).
4. Сразу после выката (до этого все посетители делят лимиты адреса Nginx):
   ```bash
   deploy/k8s/scripts/set-trusted-proxy.sh
   ```
5. Проверка:
   ```bash
   curl -fsS https://ortalyq.compserv.cloud/api/ready
   curl -fsS https://ortalyq.compserv.cloud/api/config    # "version" = SHA выпуска, "demo":{"enabled":true,"minutes":15,...}
   deploy/k8s/scripts/set-trusted-proxy.sh --check
   kubectl -n ortalyq logs deploy/backend --since=10m | grep -E 'level=(ERROR|WARN)'
   ```
   В браузере: `https://ortalyq.compserv.cloud/demo` → «Посмотреть самостоятельно» → «Продолжить без камеры и микрофона» → доска с примером, таймер; «Завершить демонстрацию» → экран «Демонстрация завершена». «Попробовать вдвоём» — ссылка открывается на втором устройстве, третий получает отказ. Обычный урок: вход в класс работает как раньше.
6. Откат:
   - образы: Actions → Deploy → `action` = `rollback` (или `deploy` с прошлым SHA). Таблицы демо остаются, старый образ их не использует;
   - доверенный прокси: `deploy/k8s/scripts/set-trusted-proxy.sh --restore` (или `--remove`);
   - манифесты обычно откатывать не нужно (старый образ не читает `DEMO_*` и `TRUSTED_PROXIES`); при необходимости: `git checkout <прошлый SHA> -- deploy/k8s && deploy/k8s/scripts/apply.sh && git checkout HEAD -- deploy/k8s`;
   - выключить только пробный урок без отката образов: коммит с `DEMO_ENABLED=false` в `deploy/k8s/base/kustomization.yaml` → на VPS `git pull --ff-only origin main && deploy/k8s/scripts/apply.sh`.

### Изменение манифестов (ресурсы, ConfigMap, новые компоненты)

CI не может менять инфраструктуру — у него нет на это прав. Изменения применяет администратор:
```bash
git pull --ff-only && deploy/k8s/scripts/apply.sh
```
`apply.sh` сохраняет текущие образы backend и frontend. При изменении ConfigMap поды перезапускаются автоматически: имя ConfigMap содержит хэш.

### Администратор и демо-данные

Пароли вводятся со скрытым вводом и не попадают в историю shell:
```bash
kubectl -n ortalyq exec -it deploy/backend -- sh -c 'printf "Пароль: "; read -rs ADMIN_PASSWORD; echo; export ADMIN_PASSWORD; server create-admin -email admin@example.kz -name "Администратор"'
# Демо-данные (по желанию; пароль демо-аккаунтов не публикуйте):
kubectl -n ortalyq exec -it deploy/backend -- sh -c 'printf "Пароль демо: "; read -rs DEMO_PASSWORD; echo; export DEMO_PASSWORD; server seed'
```

## 7. Проверка видеосвязи и записи после первого выката

1. Войдите двумя аккаунтами (на разных устройствах) → «Мои занятия» → «Войти в класс». Должны появиться видео и звук.
   - Если подключение не устанавливается, проверьте firewall (7881/tcp, 7882/udp) и `kubectl -n ortalyq logs deploy/livekit`.
2. Запустите запись с согласия обоих участников и остановите её. Статус должен дойти до «Готова», и запись должна открываться.
   - Если запись не запускается: `kubectl -n ortalyq logs deploy/egress`.
3. Egress учитывает CPU узла. На одну запись комнаты нужно около 2–3 свободных ядер, иначе Egress отклоняет запрос (в логе — `not enough CPU`).

## 8. Восстановление и откат

**Откат образа:**
- Actions → Deploy → action `rollback` — возврат к предыдущим ReplicaSet backend и frontend;
- или `deploy` с `sha` прошлого коммита.

**Схема БД при этом не откатывается.** Поэтому миграции пишутся по принципу expand/contract:
- добавление таблиц и столбцов (nullable или с DEFAULT) и индексов — можно в любом выпуске;
- удаление или переименование столбцов — только отдельным выпуском, когда ни одна работающая версия их уже не использует.

Старый образ работает с более новой схемой: `Migrate` применяет только известные ему файлы и не падает на незнакомых версиях.

**Миграция упала.**
- Приложение не изменялось, а упавшая миграция откатилась целиком (каждая выполняется в своей транзакции).
- Логи: `kubectl -n ortalyq logs job/<migrate-…>`. Исправьте миграцию новым коммитом.

**Нужно вернуть схему или данные — восстановление из резервной копии:**
```bash
kubectl -n ortalyq scale deploy/backend --replicas=0
kubectl -n ortalyq exec -i statefulset/postgres -- pg_restore -U ortalyq -d ortalyq --clean --if-exists < /srv/ortalyq/backups/<файл>.dump
kubectl -n ortalyq scale deploy/backend --replicas=1
```

**Резервные копии:**
- PostgreSQL: `deploy/k8s/scripts/backup-postgres.sh`, хранятся 14 последних. По расписанию — например, cron под devops: `0 3 * * * /home/devops/ortalyq/deploy/k8s/scripts/backup-postgres.sh`.
- Файлы и записи: копируйте каталог `/srv/ortalyq/s3` (например, `sudo rsync -a /srv/ortalyq/s3/ <куда>`).
- Храните копии вне VPS.

**Rollout завис или под в Pending/ImagePullBackOff:**
- `kubectl -n ortalyq describe pod <под>`;
- проверьте `ghcr-pull` (не истёк ли токен) — пересоздать: `kubectl -n ortalyq delete secret ghcr-pull && deploy/k8s/scripts/create-secrets.sh`.

**PV в состоянии Released** (PVC удалили, данные на диске целы). Повторная привязка:
```bash
kubectl patch pv ortalyq-postgres --type merge -p '{"spec":{"claimRef":{"uid":null,"resourceVersion":null}}}'
deploy/k8s/scripts/apply.sh
```

**Ротация секретов:**
- `APP_SECRET`, ключи LiveKit и S3: `kubectl -n ortalyq edit secret ortalyq-secrets`, затем `kubectl -n ortalyq rollout restart deploy`.
  - Для S3 сначала смените ключ в RustFS: он задаётся переменными окружения, поэтому после перезапуска rustfs примет новый.
- `POSTGRES_PASSWORD`: сначала `ALTER USER ortalyq PASSWORD …` в БД, затем обновите `POSTGRES_PASSWORD` и `DATABASE_URL` в Secret и перезапустите backend.

**Отозвать доступ CI:**
```bash
sudo truncate -s0 /var/lib/ortalyq-deploy/.ssh/authorized_keys          # SSH-ключ
kubectl -n ortalyq delete secret ortalyq-deployer-token                 # токен SA (новый создаст apply.sh)
```

**Полная переустановка** с сохранением данных: каталоги `/srv/ortalyq/*` не удаляйте. Затем выполните `setup-storage.sh` → `create-secrets.sh` → `apply.sh`. Если Secret пересоздан, `POSTGRES_PASSWORD` должен совпадать со старым — иначе восстановите старое значение.

## 9. Что проверено локально и что требует сервера

**Проверено локально:**
- `go vet` и `go test`, в том числе интеграционный тест на PostgreSQL 17: двойной запуск миграций, seed, идемпотентность `localize-demo`;
- `/api/ready` отвечает 200 с БД и 503 без неё;
- `actionlint` по всем workflow;
- `shellcheck` по скриптам;
- `kustomize build` и `kubeconform -strict` (Kubernetes 1.34) по всем манифестам, PV и Job миграций;
- `nginx -t` на 1.24 и 1.28;
- конфиги LiveKit и Egress после подстановки `$(VAR)` запускались на реальных образах (livekit v1.13.11, egress v1.15.0): оба запускаются, health отвечает 200;
- `bind_addresses` LiveKit проверен;
- сценарии `ortalyq-deploy` с заглушкой kubectl:
  - отклонение некорректных команд и инъекций;
  - успешный выкат;
  - падение миграции — приложение не тронуто;
  - падение rollout — автоматический откат;
  - status и rollback;
- проверка сборочного контекста, включая негативный тест.

**Требует сервера (не проверено):**
- реальное применение в кластере: привязка local PV, Pod Security, права SA (`make-deployer-kubeconfig.sh` проверит их сам);
- доступ Nginx хоста к ClusterIP (`render-upstreams.sh` проверит);
- адрес, с которого Nginx приходит в под бэкенда (`set-trusted-proxy.sh` определит и проверит);
- выпуск сертификатов;
- WebRTC через firewall VPS;
- запись Egress на ресурсах VPS;
- SSH-вход с принудительной командой;
- запуск workflow в GitHub Actions и публикация в GHCR.
