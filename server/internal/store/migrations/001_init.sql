-- 001_init.sql — начальная схема Spider.
-- Храним ключи устройств в base64. device_id — публичный идентификатор (hex).

-- Глобальные настройки key-value.
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Enrollment-токены: одноразовые, с TTL, создаются админом.
CREATE TABLE IF NOT EXISTS enrollments (
    token     TEXT PRIMARY KEY,
    note      TEXT NOT NULL DEFAULT '',
    key_b64   TEXT NOT NULL,            -- base64(симметричный ключ), выдаётся агенту
    pub_b64   TEXT NOT NULL,            -- base64(публичный ключ сервера X25519)
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at   INTEGER,                  -- NULL = не использован
    used_by   TEXT                      -- device_id, который активировал токен
);
CREATE INDEX IF NOT EXISTS idx_enrollments_expires ON enrollments(expires_at);

-- Устройства.
CREATE TABLE IF NOT EXISTS devices (
    device_id   TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    key_b64     TEXT NOT NULL,          -- base64(симметричный ключ сессии)
    hostname    TEXT NOT NULL DEFAULT '',
    os          TEXT NOT NULL DEFAULT '',
    arch        TEXT NOT NULL DEFAULT '',
    cpu_brand   TEXT NOT NULL DEFAULT '',
    cpu_cores   INTEGER NOT NULL DEFAULT 0,
    mem_total   INTEGER NOT NULL DEFAULT 0,    -- байт
    agent_ver   TEXT NOT NULL DEFAULT '',
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    online      INTEGER NOT NULL DEFAULT 0     -- 0/1
);
CREATE INDEX IF NOT EXISTS idx_devices_online ON devices(online);

-- Команды: сервер → устройство.
CREATE TABLE IF NOT EXISTS commands (
    id          TEXT PRIMARY KEY,       -- uuid/ulid
    device_id   TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
    command     TEXT NOT NULL,          -- shell-команда
    timeout_sec INTEGER NOT NULL DEFAULT 60,
    status      TEXT NOT NULL,          -- queued|running|done|error|timeout|cancelled
    created_at  INTEGER NOT NULL,
    dispatched_at INTEGER,
    finished_at INTEGER,
    created_by  TEXT NOT NULL DEFAULT 'admin'
);
CREATE INDEX IF NOT EXISTS idx_commands_device ON commands(device_id, status);
CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status);

-- Результаты выполнения команд.
CREATE TABLE IF NOT EXISTS results (
    command_id TEXT PRIMARY KEY REFERENCES commands(id) ON DELETE CASCADE,
    exit_code  INTEGER NOT NULL,
    stdout     TEXT NOT NULL DEFAULT '',  -- base64 произвольного вывода
    stderr     TEXT NOT NULL DEFAULT '',
    finished_at INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0
);

-- Аудит действий администратора.
CREATE TABLE IF NOT EXISTS audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    actor      TEXT NOT NULL,           -- 'admin' / device_id
    action     TEXT NOT NULL,           -- 'command.enqueue' и т.д.
    target     TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_at ON audit_log(at);

-- Значение по умолчанию для глобального тумблера команд: включено.
INSERT OR IGNORE INTO settings(key, value) VALUES ('commands_enabled', '1');
INSERT OR IGNORE INTO settings(key, value) VALUES ('enroll_server_pubkey', '');
