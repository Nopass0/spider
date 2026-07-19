-- 002_screenshots.sql — хранение скриншотов с устройств.
-- Файлы лежат в /var/lib/spider/screenshots/{device_id}/{name}, в БД — метаданные.

CREATE TABLE IF NOT EXISTS screenshots (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id   TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
    name        TEXT NOT NULL,            -- имя файла (без пути)
    w           INTEGER NOT NULL DEFAULT 0,
    h           INTEGER NOT NULL DEFAULT 0,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_screenshots_device ON screenshots(device_id, created_at);
