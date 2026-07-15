package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EnqueueCommand ставит команду в очередь для устройства. Возвращает созданную запись.
func (s *Store) EnqueueCommand(ctx context.Context, c Command) (Command, error) {
	if c.Status == "" {
		c.Status = StatusQueued
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 60
	}
	c.CreatedAt = nowUTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO commands(id, device_id, command, timeout_sec, status, created_at, created_by)
		VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.DeviceID, c.Command, c.TimeoutSec, string(c.Status),
		c.CreatedAt.Unix(), c.CreatedBy)
	if err != nil {
		return Command{}, fmt.Errorf("store: enqueue command: %w", err)
	}
	return c, nil
}

// DequeueCommands возвращает и блокирует (FOR UPDATE) queued-команды для устройства.
// Вызывается агентом при подключении/long-poll. Помечает их running.
func (s *Store) DequeueCommands(ctx context.Context, deviceID string, limit int) ([]Command, error) {
	if limit <= 0 {
		limit = 16
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, device_id, command, timeout_sec, status, created_at, dispatched_at, created_by
		FROM commands
		WHERE device_id=? AND status=?
		ORDER BY created_at ASC LIMIT ?`, deviceID, string(StatusQueued), limit)
	if err != nil {
		return nil, fmt.Errorf("store: dequeue select: %w", err)
	}
	out := make([]Command, 0)
	for rows.Next() {
		var c Command
		var created int64
		var dispatched sql.NullInt64
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Command, &c.TimeoutSec,
			&c.Status, &created, &dispatched, &c.CreatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		c.CreatedAt = time.Unix(created, 0).UTC()
		if dispatched.Valid {
			t := time.Unix(dispatched.Int64, 0).UTC()
			c.DispatchedAt = &t
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := nowUTC().Unix()
	for i := range out {
		if _, err := tx.ExecContext(ctx,
			`UPDATE commands SET status=?, dispatched_at=? WHERE id=?`,
			string(StatusRunning), now, out[i].ID); err != nil {
			return nil, fmt.Errorf("store: mark running: %w", err)
		}
		st := nowUTC()
		out[i].Status = StatusRunning
		out[i].DispatchedAt = &st
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit dequeue: %w", err)
	}
	return out, nil
}

// SaveResult сохраняет результат выполнения команды и переводит её в финальный статус.
func (s *Store) SaveResult(ctx context.Context, cmdID string, status CommandStatus, r Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := nowUTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE commands SET status=?, finished_at=? WHERE id=?`,
		string(status), now.Unix(), cmdID); err != nil {
		return fmt.Errorf("store: finalize command: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO results(command_id, exit_code, stdout, stderr, finished_at, duration_ms)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(command_id) DO UPDATE SET
			exit_code=excluded.exit_code, stdout=excluded.stdout, stderr=excluded.stderr,
			finished_at=excluded.finished_at, duration_ms=excluded.duration_ms`,
		cmdID, r.ExitCode, r.StdoutB64, r.StderrB64, r.FinishedAt.Unix(), r.DurationMs); err != nil {
		return fmt.Errorf("store: save result: %w", err)
	}
	return tx.Commit()
}

// GetCommand возвращает команду по ID.
func (s *Store) GetCommand(ctx context.Context, id string) (Command, error) {
	var c Command
	var created int64
	var dispatched, finished sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, device_id, command, timeout_sec, status, created_at, dispatched_at, finished_at, created_by
		FROM commands WHERE id=?`, id).Scan(
		&c.ID, &c.DeviceID, &c.Command, &c.TimeoutSec, &c.Status, &created,
		&dispatched, &finished, &c.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Command{}, ErrCommandNotFound
	}
	if err != nil {
		return Command{}, fmt.Errorf("store: get command: %w", err)
	}
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.DispatchedAt = nullTime(dispatched)
	c.FinishedAt = nullTime(finished)
	return c, nil
}

// GetResult возвращает результат команды (если есть).
func (s *Store) GetResult(ctx context.Context, cmdID string) (Result, bool, error) {
	var r Result
	var finished int64
	err := s.db.QueryRowContext(ctx, `
		SELECT command_id, exit_code, stdout, stderr, finished_at, duration_ms
		FROM results WHERE command_id=?`, cmdID).Scan(
		&r.CommandID, &r.ExitCode, &r.StdoutB64, &r.StderrB64, &finished, &r.DurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("store: get result: %w", err)
	}
	r.FinishedAt = time.Unix(finished, 0).UTC()
	return r, true, nil
}

// ListDeviceCommands возвращает историю команд устройства (свежие сверху).
func (s *Store) ListDeviceCommands(ctx context.Context, deviceID string, limit int) ([]Command, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, command, timeout_sec, status, created_at, dispatched_at, finished_at, created_by
		FROM commands WHERE device_id=? ORDER BY created_at DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list commands: %w", err)
	}
	defer rows.Close()
	out := make([]Command, 0)
	for rows.Next() {
		var c Command
		var created int64
		var dispatched, finished sql.NullInt64
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Command, &c.TimeoutSec, &c.Status,
			&created, &dispatched, &finished, &c.CreatedBy); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.DispatchedAt = nullTime(dispatched)
		c.FinishedAt = nullTime(finished)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CancelQueued помечает все queued-команды устройства отменёнными (при отключении).
func (s *Store) CancelQueued(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status=?, finished_at=? WHERE device_id=? AND status=?`,
		string(StatusCancelled), nowUTC().Unix(), deviceID, string(StatusQueued))
	if err != nil {
		return fmt.Errorf("store: cancel queued: %w", err)
	}
	return nil
}

// ErrCommandNotFound — команда не найдена.
var ErrCommandNotFound = errors.New("command not found")

// nullTime конвертирует sql.NullInt64 в *time.Time — DRY.
func nullTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0).UTC()
	return &t
}
