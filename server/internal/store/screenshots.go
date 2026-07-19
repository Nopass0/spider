package store

import (
	"context"
	"fmt"
)

// Screenshot — метаданные сохранённого скриншота.
type Screenshot struct {
	ID        int64  `json:"id"`
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt int64  `json:"created_at"`
}

// SaveScreenshot сохраняет метаданные скриншота. Файл должен быть уже записан.
func (s *Store) SaveScreenshot(ctx context.Context, deviceID, name string, w, h int, sizeBytes int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO screenshots(device_id, name, w, h, size_bytes, created_at)
		VALUES(?,?,?,?,?,?)`,
		deviceID, name, w, h, sizeBytes, nowUTC().Unix())
	if err != nil {
		return 0, fmt.Errorf("store: save screenshot: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListScreenshots возвращает последние N скриншотов устройства.
func (s *Store) ListScreenshots(ctx context.Context, deviceID string, limit int) ([]Screenshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, name, w, h, size_bytes, created_at
		FROM screenshots WHERE device_id=? ORDER BY created_at DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list screenshots: %w", err)
	}
	defer rows.Close()
	out := make([]Screenshot, 0)
	for rows.Next() {
		var sc Screenshot
		if err := rows.Scan(&sc.ID, &sc.DeviceID, &sc.Name, &sc.W, &sc.H, &sc.SizeBytes, &sc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// GetScreenshot возвращает метаданные скриншота по id.
func (s *Store) GetScreenshot(ctx context.Context, id int64) (Screenshot, bool, error) {
	var sc Screenshot
	err := s.db.QueryRowContext(ctx, `
		SELECT id, device_id, name, w, h, size_bytes, created_at
		FROM screenshots WHERE id=?`, id).Scan(
		&sc.ID, &sc.DeviceID, &sc.Name, &sc.W, &sc.H, &sc.SizeBytes, &sc.CreatedAt)
	if err != nil {
		return Screenshot{}, false, nil
	}
	return sc, true, nil
}
