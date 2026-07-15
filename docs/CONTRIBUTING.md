# Разработка Spider

## Локальное окружение

```bash
# Сервер (Go 1.25)
cd server
cp ../.env.example .env       # отредактируйте ADMIN_KEY
go run ./cmd/spider            # http://localhost:8080

# Панель (Node 20+) — hot reload
cd panel
npm install
npm run dev                    # http://localhost:5173 (проксирует /admin на :8080)

# Клиент (Rust)
cd client
cargo run -- run --server http://localhost:8080 --enroll-token <TOKEN> --yes
```

## Принципы кода

- **DRY** — общие форматы и хелперы в одном месте
  (envelope в `crypto`, wire-структуры в `proto`/`commands`, `cn`/`b64` в `lib/utils`).
- **KISS** — без избыточной абстракции; простые понятные реализации.
- **Документация** — JSDoc (TS) и rustdoc/комментарии (Rust/Go) на все модули.
- **Тесты обязательны** — `go test`, `cargo test`, `npm test` должны быть зелёными.

## Коммиты

Conventional Commits:
```
feat(server): добавить ...
fix(panel): исправить ...
docs: обновить DEPLOY
```

## Перед PR/пушем

```bash
# Сервер
cd server && go vet ./... && go test ./...

# Клиент
cd client && cargo test --all-features

# Панель
cd panel && npm test && npm run build
```

## Hot reload (dev)

- **Панель**: Vite HMR — изменения видны мгновенно.
- **Сервер**: `air` (`go install github.com/air-verse/air@latest && air`).
- **Клиент**: `cargo watch -x run` (`cargo install cargo-watch`).

## Prod-сборка

Через CI при теге `v*` (release) или пуше в `main` (deploy). См.
`.github/workflows/` и `docs/DEPLOY.md`.
