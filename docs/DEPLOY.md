# Деплой Spider

## ⚠️ Перед стартом — безопасность

Секреты **никогда** не хранятся в репозитории. Все чувствительные значения
передаются через **GitHub Actions secrets**. Перед первым деплоем:

1. **Смените root-пароль сервера** — креды могли попасть в чат при обсуждении.
2. **Сгенерируйте новый ADMIN_KEY** (минимум 32 символа):
   ```bash
   openssl rand -hex 32
   ```

## Шаг 0. Задайте GitHub Actions secrets

В репозитории `github.com/Nopass0/spider` → Settings → Secrets and variables →
Actions → New repository secret. Создайте:

| Secret              | Назначение                                  |
|---------------------|---------------------------------------------|
| `SSH_HOST`          | Хост сервера (`193.41.5.130`)               |
| `SSH_USER`          | SSH-пользователь (`root`)                   |
| `SSH_PASSWORD`      | Пароль SSH (**смените перед деплоем!**)     |
| `SPIDER_ADMIN_KEY`  | Токен админа (минимум 8 символов, лучше длиннее) |
| `SIGNING_KEY`       | Приватный ed25519-ключ подписи клиента (hex)|
| `SIGNING_PUBKEY`    | Публичный ed25519-ключ (hex, 32 байта)      |

> Если `gh` CLI установлен, задать секреты можно командами:
> ```bash
> gh secret set SSH_HOST       --body "193.41.5.130"
> gh secret set SSH_USER       --body "root"
> gh secret set SSH_PASSWORD   --body "<новый пароль>"
> gh secret set SPIDER_ADMIN_KEY --body "<новый ключ>"
> ```
> Для ed25519 пары (подпись обновлений клиента):
> ```bash
> openssl genpkey -algorithm ED25519 -out /tmp/ed.pem
> # приватный (hex) → SIGNING_KEY:
> openssl pkey -in /tmp/ed.pem -outform DER | tail -c 32 | xxd -p -c 64
> # публичный (hex, 32 байта) → SIGNING_PUBKEY:
> openssl pkey -in /tmp/ed.pem -pubout -outform DER | tail -c 32 | xxd -p -c 64
> ```

## Шаг 1. Автоматический деплой (через CI)

При пуше в `main` срабатывает workflow `.github/workflows/deploy.yml`:
1. Собирает панель (`npm run build`).
2. Встраивает панель в Go-бинарь (`go:embed`).
3. Собирает сервер (`CGO_ENABLED=0` — статический бинарь).
4. По SSH заливает бинарь + `install.sh` и запускает установку.

`install.sh` — idempotent, ставит Caddy, создаёт пользователя `spider`, каталоги,
генерирует `/etc/spider/server.env` (права 0600), systemd-unit, перезапускает сервисы.

После деплоя панель доступна на `https://spider.lowkey.su`.

## Шаг 2. Ручной деплой (если CI недоступен)

```bash
# На машине-сборщике:
cd panel && npm ci && npm run build
cd ../server
rm -rf internal/panel/dist && cp -r ../../panel/dist internal/panel/dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o spider-server ./cmd/spider

# Залить на сервер:
scp spider-server deploy/install.sh deploy/Caddyfile deploy/spider-server.service \
    root@193.41.5.130:/tmp/spider-deploy/

# На сервере:
ssh root@193.41.5.130
SPIDER_ADMIN_KEY="<ключ>" bash /tmp/spider-deploy/install.sh /tmp/spider-deploy/spider-server
```

## Сертификаты

### Стандартный (HTTP-01) — автоматически
Caddy автоматически выпускает сертификат Let's Encrypt для `spider.lowkey.su`
через HTTP-01 при первом старте. Ничего делать не нужно — домен должен смотреть
A-записью на IP сервера (193.41.5.130), порты 80/443 открыты.

### Wildcard `*.spider.lowkey.su` (DNS-01) — вручную
Wildcard требует DNS-01 challenge (HTTP-01 не поддерживает wildcard).
Поскольку DNS управляется вручную, процедура ручная:

```bash
# 1. Установить certbot на сервере
apt install -y certbot

# 2. Выпуск wildcard (потребуется временно добавить TXT-запись в DNS
#    _acme-challenge.spider.lowkey.su — certbot подскажет значение):
certbot certonly --manual --preferred-challenges dns -d '*.spider.lowkey.su' -d spider.lowkey.su

# 3. Сказать Caddy использовать выпущенные сертификаты (в Caddyfile):
#    spider.lowkey.su {
#      tls /etc/letsencrypt/live/spider.lowkey.su/fullchain.pem \
#          /etc/letsencrypt/live/spider.lowkey.su/privkey.pem
#      reverse_proxy 127.0.0.1:8080
#    }
systemctl restart caddy
```

Перевыпуск wildcard нужно повторять раз в 60-90 дней вручную (или автоматизировать
через DNS-API провайдера, если появится).

## Проверка после деплоя

```bash
curl https://spider.lowkey.su/healthz          # → {"status":"ok"}
journalctl -u spider-server -e --no-pager      # логи сервера
systemctl status spider-server caddy
```

Войдите в панель → введите ADMIN_KEY → создайте enrollment-токен →
запустите агента на машине пула (см. `docs/CLIENT.md`).

## Откат

```bash
# На сервере откатить бинарь на предыдущую версию и перезапустить:
cp /usr/local/bin/spider-server.bak /usr/local/bin/spider-server
systemctl restart spider-server
```
`install.sh` можно доработать под бэкап перед обновлением (оставлено как TODO).
