#!/usr/bin/env bash
# =============================================================================
# install.sh — установка/обновление сервера Spider на чистый Linux-сервер.
#
# Что делает (idempotent — можно запускать многократно):
#   1. Ставит системные зависимости (curl, ca-certificates, Caddy).
#   2. Создает пользователя spider и каталоги (/var/lib/spider, /var/log/spider).
#   3. Кладёт бинарь сервера в /usr/local/bin/spider-server.
#   4. Генерирует /etc/spider/server.env из переменных окружения (секреты!).
#   5. Устанавливает systemd-unit и Caddyfile.
#   6. Перезапускает сервисы.
#
# Запускать под root (деплой через SSH из CI). Секреты передаются через env.
# =============================================================================
set -euo pipefail

DOMAIN="spider.lowkey.su"
INSTALL_BIN="/usr/local/bin/spider-server"
ENV_FILE="/etc/spider/server.env"
DATA_DIR="/var/lib/spider"
LOG_DIR="/var/log/spider"

log()  { printf '\033[1;36m[spider]\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31m[ошибка]\033[0m %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

require_root() {
  [ "$(id -u)" -eq 0 ] || die "запускайте под root (sudo)."
}

# --- проверка обязательных переменных ---
check_env() {
  : "${SPIDER_ADMIN_KEY:?SPIDER_ADMIN_KEY обязательна (см. .env.example)}"
  log "конфигурация: domain=$DOMAIN admin_key=***"
}

# --- зависимости ---
install_deps() {
  log "обновление индексов пакетов…"
  apt-get update -qq

  log "установка системных пакетов…"
  apt-get install -y -qq curl ca-certificates debian-keyring debian-archive-keyring apt-transport-https >/dev/null

  if ! command -v caddy >/dev/null 2>&1; then
    log "установка Caddy (официальный репозиторий)…"
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' -o /etc/apt/trusted.gpg.d/caddy-stable.gpg 2>/dev/null || true
    echo 'deb [trusted=yes] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main' \
      > /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -qq
    apt-get install -y -qq caddy >/dev/null
  fi
  log "Caddy: $(caddy version 2>/dev/null || echo 'установлен')"
}

# --- каталоги и пользователь ---
setup_dirs() {
  if ! id -u spider >/dev/null 2>&1; then
    log "создание системного пользователя spider…"
    useradd --system --no-create-home --shell /usr/sbin/nologin spider
  fi
  install -d -o spider -g spider -m 0750 "$DATA_DIR" "$LOG_DIR"
  install -d -o root -g root -m 0750 "$(dirname "$ENV_FILE")"
  # Логи Caddy сейчас идут в journald, но если админ вернёт file-лог —
  # дадим пользователю caddy доступ к /var/log/spider через группу.
  if id -u caddy >/dev/null 2>&1; then
    usermod -aG spider caddy 2>/dev/null || true
  fi
}

# --- бинарь сервера ---
install_binary() {
  # Источник: аргумент $1 (путь к локальному бинарю) или скачивание из GH Release.
  local src="${1:-}"
  if [ -n "$src" ] && [ -f "$src" ]; then
    log "установка бинаря из $src…"
    install -m 0755 "$src" "$INSTALL_BIN"
  elif [ -n "${SPIDER_BIN_URL:-}" ]; then
    log "скачивание бинаря из $SPIDER_BIN_URL…"
    curl -fsSL "$SPIDER_BIN_URL" -o "$INSTALL_BIN"
    chmod 0755 "$INSTALL_BIN"
  else
    die "нет бинаря: передайте путь аргументом или задайте SPIDER_BIN_URL"
  fi
}

# --- конфигурация из env (секреты НЕ логируются) ---
write_env() {
  log "генерация $ENV_FILE …"
  cat > "$ENV_FILE" <<EOF
# Сгенерировано install.sh. Права 0600 — файл содержит ADMIN_KEY.
SPIDER_ADMIN_KEY=${SPIDER_ADMIN_KEY}
SPIDER_HTTP_ADDR=:8080
SPIDER_PUBLIC_URL=https://${DOMAIN}
SPIDER_DB_PATH=${DATA_DIR}/spider.db
SPIDER_LONGPOLL_TIMEOUT=30
SPIDER_ENROLL_TTL_HOURS=24
SPIDER_LOG_LEVEL=${SPIDER_LOG_LEVEL:-info}
EOF
  chmod 0600 "$ENV_FILE"
  chown root:spider "$ENV_FILE"
}

# --- systemd ---
install_systemd() {
  log "установка systemd-unit…"
  install -m 0644 "$(dirname "$0")/spider-server.service" \
    /etc/systemd/system/spider-server.service
  systemctl daemon-reload
}

# --- Caddy ---
install_caddy() {
  log "установка Caddyfile (полная замена)…"
  install -d -m 0755 /etc/caddy
  # Бэкапим старый конфиг на всякий случай.
  [ -f /etc/caddy/Caddyfile ] && cp -a /etc/caddy/Caddyfile /etc/caddy/Caddyfile.spider.bak
  # Принудительно перезаписываем нашим конфигом.
  cat "$(dirname "$0")/Caddyfile" > /etc/caddy/Caddyfile
  # Caddy сам выпустит сертификат Let's Encrypt при старте.

  # Если файл заблокирован immutable или read-only — снимаем.
  chattr -i /etc/caddy/Caddyfile 2>/dev/null || true
  chmod 0644 /etc/caddy/Caddyfile
}

# --- файрвол: убедиться, что 80/443 открыты ---
open_firewall() {
  if command -v ufw >/dev/null 2>&1; then
    log "открытие портов 80/443 в ufw…"
    ufw allow 80/tcp >/dev/null 2>&1 || true
    ufw allow 443/tcp >/dev/null 2>&1 || true
  fi
  # iptables-nft напрямую не трогаем — пусть админ решает.
}

# --- запуск ---
start_services() {
  log "запуск spider-server…"
  systemctl enable spider-server
  systemctl restart spider-server
  log "применение Caddyfile…"
  # Сначала проверяем синтаксис, затем перезапускаем Caddy целиком
  # (reload может не подхватить смену домена/сертификата).
  if caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile 2>/dev/null; then
    systemctl enable caddy 2>/dev/null || true
    systemctl restart caddy
  else
    err "Caddyfile невалиден; проверьте /etc/caddy/Caddyfile"
    caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile || true
  fi
  sleep 2
  local rc=0
  if systemctl is-active --quiet spider-server; then
    log "✓ spider-server активен"
  else
    err "spider-server не стартовал; проверьте: journalctl -u spider-server -e"
    rc=1
  fi
  if systemctl is-active --quiet caddy; then
    log "✓ caddy активен"
  else
    err "caddy не стартовал; проверьте: journalctl -u caddy -e"
    rc=1
  fi
  return $rc
}

main() {
  require_root
  check_env
  install_deps
  setup_dirs
  install_binary "$@"
  write_env
  install_systemd
  install_caddy
  open_firewall
  start_services
  log "✓ установка завершена. Панель: https://${DOMAIN}"
}

main "$@"
