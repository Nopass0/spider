# Spider

Удалённое консольное управление **собственным** парком машин: Go-сервер с веб-панелью и Rust-клиент (Windows / Linux x86_64).

> ⚠️ **Назначение.** Инструмент создан исключительно для администрирования машин, которыми вы владеете. Не используйте на чужих устройствах без явного разрешения владельца.

## Возможности

- 🖥️ **Удалённая консоль** — отправка shell-команд, получение stdout/stderr/exit-кода.
- 🔐 **Сквозное шифрование** — TLS (Caddy) + прикладной слой: X25519 ECDH → HKDF-SHA256 → AES-256-GCM на каждое сообщение.
- 🔁 **Два транспорта, один протокол** — WebSocket (основной) и long-poll (fallback) с общим форматом сообщений.
- 🪪 **Enrollment вместо отпечатка** — каждое устройство регистрируется одноразовым токеном и получает стабильный ID.
- 🧩 **Видимый системный сервис** — systemd unit / Windows Service, без скрытости и тихого закрепления.
- ✍️ **Подписанное автообновление** — клиент проверяет ed25519-подпись перед установкой новой версии.
- 🌐 **Веб-панель** — тёмная, tailwind + shadcn/ui + zustand + zod + lucide-react + motion.
- 🚀 **Автодеплой** — один скрипт ставит сервер на чистую машину.

## Состав

| Каталог   | Что это                                    | Стек                          |
|-----------|--------------------------------------------|-------------------------------|
| `server/` | API, WebSocket-hub, очередь команд, SQLite | Go 1.25                       |
| `client/` | Агент-исполнитель                          | Rust (rustls, без CGO)        |
| `panel/`  | Веб-панель администратора                  | Vite + React + TS             |
| `deploy/` | Скрипты установки, Caddyfile, systemd      | bash                          |
| `docs/`   | Документация                               | Markdown                      |

## Быстрый старт (локальная разработка)

```bash
# 1. Сервер
cd server && cp ../.env.example .env   # отредактируйте ADMIN_KEY
go run ./cmd/spider                     # http://localhost:8080

# 2. Панель (hot-reload)
cd panel && npm install && npm run dev  # http://localhost:5173

# 3. Клиент (нужен enrollment-токен, см. docs/CLIENT.md)
cd client && cargo run -- run --server http://localhost:8080 --enroll-token <TOKEN>
```

## Документация

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — компоненты, потоки данных, протокол.
- [`docs/API.md`](docs/API.md) — admin и agent эндпоинты.
- [`docs/CLIENT.md`](docs/CLIENT.md) — установка, фичи сборки, автозапуск, обновление.
- [`docs/DEPLOY.md`](docs/DEPLOY.md) — прод-деплой, Caddy, certbot, секреты.
- [`docs/SECURITY.md`](docs/SECURITY.md) — модель угроз и меры защиты.

## Лицензия

Proprietary — только для внутреннего использования.
