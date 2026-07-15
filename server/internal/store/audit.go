package store

import (
	"context"
	"fmt"
)

// AppendAudit добавляет запись в журнал аудита.
func (s *Store) AppendAudit(ctx context.Context, actor, action, target, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log(actor, action, target, detail, at) VALUES(?,?,?,?,?)`,
		actor, action, target, detail, nowUTC().Unix())
	if err != nil {
		return fmt.Errorf("store: append audit: %w", err)
	}
	return nil
}

// ListAudit возвращает последние N записей аудита (свежие сверху).
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor, action, target, detail, at
		FROM audit_log ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list audit: %w", err)
	}
	defer rows.Close()
	out := make([]AuditEntry, 0)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
