# Клиент Spider (агент)

Rust-агент для Windows x86_64 и Linux x86_64. **Один бинарь без внешних
зависимостей** (rustls вместо OpenSSL, SQLite-драйвер pure-Go на сервере).

## Сборка

```bash
cd client

# Dev (локально, текущая ОС):
cargo build

# Release (оптимизация размера):
cargo build --release

# Конкретный таргет:
rustup target add x86_64-pc-windows-gnu
cargo build --release --target x86_64-pc-windows-gnu

rustup target add x86_64-unknown-linux-gnu
cargo build --release --target x86_64-unknown-linux-gnu
```

CI (`.github/workflows/release.yml`) собирает оба таргета при теге `v*` и
публикует в GitHub Release с ed25519-подписью.

## Feature-флаги

`Cargo.toml`:
```toml
[features]
default = ["autostart", "self-update"]
autostart = []                       # добавление/удаление из автозапуска
self-update = [...]                  # автообновление с проверкой подписи
```

- Собрать без автозапуска: `cargo build --no-default-features`
- Собрать без автообновления: `cargo build --no-default-features --features autostart`

## Использование

### Регистрация (первый запуск)
Нужен enrollment-токен (создаётся в панели → Enrollments).

```bash
# Линукс:
./spider-agent run --server https://spider.lowkey.su --enroll-token <TOKEN>

# Windows:
spider-agent.exe run --server https://spider.lowkey.su --enroll-token <TOKEN>
```

При первом запуске агент покажет предупреждение и попросит подтверждение
(`y/N`). Для автоматизации — флаг `--yes`.

После регистрации создаётся `spider-state.toml` (рядом с бинарем) с `device_id`
и ключом сессии. Его **не удалять** — иначе придётся перерегистрироваться.

### Команды

```bash
spider-agent run          # основной режим (enroll + транспорт, бесконечно)
spider-agent status       # показать device_id, сервер, версию
spider-agent autostart install   # добавить в автозапуск (systemd / HKCU Run)
spider-agent autostart remove    # убрать из автозапуска
spider-agent autostart status    # статус автозапуска
spider-agent update --from <URL> # применить обновление (проверка подписи)
```

### Environment-переменные (для сервисов/автозапуска)

| Переменная          | Назначение                       |
|---------------------|----------------------------------|
| `SPIDER_STATE`      | Путь к state-файлу               |
| `SPIDER_SERVER`     | Адрес сервера                    |
| `SPIDER_ENROLL_TOKEN` | Токен регистрации (первый запуск) |
| `RUST_LOG`          | Уровень логирования (`info`)     |

## Автозапуск

**Видимый, задокументированный системный сервис** — не скрытый.

- **Linux**: systemd-unit `/etc/systemd/system/spider-agent.service`
  (`Restart=on-failure`, `WantedBy=multi-user.target`).
- **Windows**: ключ реестра `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
  (видимый, управляемый).

Установка/удаление — через `spider-agent autostart install|remove`.

## Автообновление

Только с feature `self-update`. Процесс:
1. Скачать zip-архив новой версии + `.sig` (ed25519-подпись архива).
2. Проверить подпись против вшитого `SIGNING_PUBKEY`.
3. Распаковать бинарь, атомарно заменить текущий.
4. Перезапустить через сервис-менеджер.

> ⚠️ Без валидной подписи обновление **не применяется**. Публичный ключ
> `SIGNING_PUBKEY` вшивается при релизной сборке (`SIGNING_PUBKEY` env).
> В dev-сборке ключ пуст → обновления отключены (безопасно).

## Предупреждение первого запуска

При первом запуске агент выводит в stderr предупреждение и запрашивает `y/N`:

```
==========================================================================
  Spider Agent — утилита удалённого управления.
  Эта программа зарегистрирует ТЕКУЩУЮ машину на сервере Spider и позволит
  администратору выполнять на ней консольные команды.
  ...
==========================================================================
Продолжить регистрацию? [y/N]:
```

В неинтерактивном режиме (служба/скрипт) подтверждение требуется через `--yes`.
