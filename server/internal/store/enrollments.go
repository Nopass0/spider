package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateEnrollment сохраняет новый enrollment-токен.
// keyB64/pubB64 — заранее сгенерированные ключи (см. пакет crypto).
func (s *Store) CreateEnrollment(ctx context.Context, token, note, keyB64, pubB64 string, ttl time.Duration) error {
	now := nowUTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO enrollments(token, note, key_b64, pub_b64, created_at, expires_at)
		VALUES(?,?,?,?,?,?)`,
		token, note, keyB64, pubB64, now.Unix(), now.Add(ttl).Unix())
	if err != nil {
		return fmt.Errorf("store: create enrollment: %w", err)
	}
	return nil
}

// ConsumeEnrollment атомарно погашает токен: возвращает ключи, если токен
// валиден и ещё не использован. Помечает токен использованным с device_id.
func (s *Store) ConsumeEnrollment(ctx context.Context, token, deviceID string) (keyB64, pubB64 string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var keyB, pubB string
	var usedAt sql.NullInt64
	var expiresAt int64
	row := tx.QueryRowContext(ctx, `
		SELECT key_b64, pub_b64, used_at, expires_at
		FROM enrollments WHERE token=?`, token)
	if err := row.Scan(&keyB, &pubB, &usedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrEnrollmentNotFound
		}
		return "", "", fmt.Errorf("store: query enrollment: %w", err)
	}
	if usedAt.Valid {
		return "", "", ErrEnrollmentAlreadyUsed
	}
	if nowUTC().Unix() > expiresAt {
		return "", "", ErrEnrollmentExpired
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE enrollments SET used_at=?, used_by=? WHERE token=?`,
		nowUTC().Unix(), deviceID, token); err != nil {
		return "", "", fmt.Errorf("store: consume enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("store: commit: %w", err)
	}
	return keyB, pubB, nil
}

// ListEnrollments возвращает все enrollment-токены (без секретов), свежие сверху.
func (s *Store) ListEnrollments(ctx context.Context) ([]Enrollment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token, note, pub_b64, created_at, expires_at, used_at, used_by
		FROM enrollments ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list enrollments: %w", err)
	}
	defer rows.Close()
	out := make([]Enrollment, 0)
	for rows.Next() {
		var e Enrollment
		var created, expires int64
		var usedAt sql.NullInt64
		var usedBy sql.NullString
		if err := rows.Scan(&e.Token, &e.Note, &e.PubB64, &created, &expires, &usedAt, &usedBy); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0).UTC()
		e.ExpiresAt = time.Unix(expires, 0).UTC()
		if usedAt.Valid {
			t := time.Unix(usedAt.Int64, 0).UTC()
			e.UsedAt = &t
		}
		if usedBy.Valid {
			e.UsedBy = usedBy.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEnrollment удаляет токен по значению.
func (s *Store) DeleteEnrollment(ctx context.Context, token string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM enrollments WHERE token=?`, token)
	if err != nil {
		return fmt.Errorf("store: delete enrollment: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEnrollmentNotFound
	}
	return nil
}

// Ошибки enrollment-операций.
var (
	ErrEnrollmentNotFound    = errors.New("enrollment token not found")
	ErrEnrollmentAlreadyUsed = errors.New("enrollment token already used")
	ErrEnrollmentExpired     = errors.New("enrollment token expired")
)
