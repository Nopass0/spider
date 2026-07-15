package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpsertDevice создаёт или обновляет устройство по device_id.
// При регистрации передаётся полный набор инфо; при heartbeat обновляются
// только динамические поля (last_seen, online, сист.инфо).
func (s *Store) UpsertDevice(ctx context.Context, d Device) error {
	now := nowUTC().Unix()
	first := now
	if d.FirstSeen.IsZero() {
		d.FirstSeen = nowUTC()
		first = d.FirstSeen.Unix()
	}
	online := 0
	if d.Online {
		online = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO devices(
			device_id, name, key_b64, hostname, os, arch, cpu_brand, cpu_cores,
			mem_total, agent_ver, first_seen, last_seen, online
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET
			hostname=excluded.hostname, os=excluded.os, arch=excluded.arch,
			cpu_brand=excluded.cpu_brand, cpu_cores=excluded.cpu_cores,
			mem_total=excluded.mem_total, agent_ver=excluded.agent_ver,
			last_seen=excluded.last_seen, online=excluded.online`,
		d.DeviceID, d.Name, d.KeyB64, d.Hostname, d.OS, d.Arch, d.CPUBrand,
		d.CPUCores, d.MemTotal, d.AgentVer, first, now, online)
	if err != nil {
		return fmt.Errorf("store: upsert device: %w", err)
	}
	return nil
}

// TouchDevice отмечает устройство онлайн и обновляет last_seen.
func (s *Store) TouchDevice(ctx context.Context, deviceID string, online bool) error {
	v := 0
	if online {
		v = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE devices SET last_seen=?, online=? WHERE device_id=?`,
		nowUTC().Unix(), v, deviceID)
	if err != nil {
		return fmt.Errorf("store: touch device: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// GetDevice возвращает устройство по ID (с ключом сессии).
func (s *Store) GetDevice(ctx context.Context, deviceID string) (Device, error) {
	var d Device
	var first, last int64
	var online int
	err := s.db.QueryRowContext(ctx, `
		SELECT device_id, name, key_b64, hostname, os, arch, cpu_brand, cpu_cores,
		       mem_total, agent_ver, first_seen, last_seen, online
		FROM devices WHERE device_id=?`, deviceID).Scan(
		&d.DeviceID, &d.Name, &d.KeyB64, &d.Hostname, &d.OS, &d.Arch, &d.CPUBrand,
		&d.CPUCores, &d.MemTotal, &d.AgentVer, &first, &last, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrDeviceNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("store: get device: %w", err)
	}
	d.FirstSeen = time.Unix(first, 0).UTC()
	d.LastSeen = time.Unix(last, 0).UTC()
	d.Online = online == 1
	return d, nil
}

// GetSessionKey возвращает base64-ключ сессии устройства (для (де)шифровки).
func (s *Store) GetSessionKey(ctx context.Context, deviceID string) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx,
		`SELECT key_b64 FROM devices WHERE device_id=?`, deviceID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDeviceNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: get session key: %w", err)
	}
	return key, nil
}

// ListDevices возвращает все устройства, свежие по last_seen сверху.
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_id, name, hostname, os, arch, cpu_brand, cpu_cores,
		       mem_total, agent_ver, first_seen, last_seen, online
		FROM devices ORDER BY online DESC, last_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var first, last int64
		var online int
		if err := rows.Scan(&d.DeviceID, &d.Name, &d.Hostname, &d.OS, &d.Arch,
			&d.CPUBrand, &d.CPUCores, &d.MemTotal, &d.AgentVer, &first, &last, &online); err != nil {
			return nil, err
		}
		d.FirstSeen = time.Unix(first, 0).UTC()
		d.LastSeen = time.Unix(last, 0).UTC()
		d.Online = online == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDeviceName устанавливает человекочитаемое имя устройства.
func (s *Store) SetDeviceName(ctx context.Context, deviceID, name string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE devices SET name=? WHERE device_id=?`, name, deviceID)
	if err != nil {
		return fmt.Errorf("store: set device name: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// DeleteDevice удаляет устройство каскадно (команды/результаты).
func (s *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE device_id=?`, deviceID)
	if err != nil {
		return fmt.Errorf("store: delete device: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// MarkAllOffline сбрасывает флаг online у всех устройств (при старте сервера).
func (s *Store) MarkAllOffline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devices SET online=0`)
	if err != nil {
		return fmt.Errorf("store: mark offline: %w", err)
	}
	return nil
}

// ErrDeviceNotFound — устройство не найдено.
var ErrDeviceNotFound = errors.New("device not found")
