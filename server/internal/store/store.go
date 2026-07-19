// Package store реализует персистентное хранилище сервера на SQLite (pure-Go
// драйвер modernc.org/sqlite — без CGO, статическая сборка).
//
// Сущности:
//
//	enrollments — одноразовые токены регистрации устройств (создаются админом).
//	devices     — зарегистрированные устройства (device_id + общий ключ).
//	commands    — очередь команд сервер → устройство.
//	results     — результаты выполнения команд.
//	audit_log   — аудит действий администратора.
//
// Все методы потокобезопасны на уровне sql.DB (пул соединений с мьютексом).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store — обёртка над *sql.DB с типизированными методами.
type Store struct {
	db *sql.DB
}

// New открывает (или создаёт) базу по пути path и применяет миграции.
// В тестах удобно передать ":memory:" или file-URI с cache=shared.
func New(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// SQLite хорошо работает с одним писателем — ограничиваем пул соединений.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// applyMigrations применяет все .sql файлы из migrations/ в алфавитном порядке.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
	}
	return nil
}

// Close закрывает соединение с БД.
func (s *Store) Close() error { return s.db.Close() }

// DB возвращает underlying *sql.DB (для низкоуровневых операций/тестов).
func (s *Store) DB() *sql.DB { return s.db }

// ===========================================================================
// Системные/глобальные настройки (key-value)
// ===========================================================================

// GetSetting возвращает строковое значение настройки или def.
func (s *Store) GetSetting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting устанавливает (upsert) строковую настройку.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}

// ===========================================================================
// Время — единый хелпер для согласованности (DRY)
// ===========================================================================

// nowUTC возвращает текущее время в UTC (обёртка для моков в тестах).
var nowUTC = func() time.Time { return time.Now().UTC() }
