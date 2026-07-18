// Package commands связывает очередь команд (store), реестр соединений (hub) и
// крипто-слой. Это "мозг" диспетчеризации:
//
//   - Dispatch() ставит команду в БД и пытается мгновенно доставить через WS hub.
//     Если устройство онлайн — отправляет шифрованный envelope; иначе команда
//     остаётся queued и заберётся при следующем подключении/long-poll.
//   - HandleResult() принимает результат от агента, сохраняет и эмитит событие панели.
//
// Здесь же живут wire-структуры сообщений, общие для сервера и клиента.
package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/nopass0/spider/server/internal/crypto"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// WireCommand — payload сообщения MsgCommand (сервер → агент).
type WireCommand struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec"`
}

// WireResult — payload сообщения MsgCommandResult (агент → сервер).
type WireResult struct {
	CommandID  string `json:"command_id"`
	ExitCode   int    `json:"exit_code"`
	StdoutB64  string `json:"stdout_b64"`
	StderrB64  string `json:"stderr_b64"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// WireHeartbeat — payload сообщения MsgHeartbeat (агент → сервер).
type WireHeartbeat struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUBrand string `json:"cpu_brand"`
	CPUCores int    `json:"cpu_cores"`
	MemTotal uint64 `json:"mem_total"`
	AgentVer string `json:"agent_version"`
}

// WireServerInfo — payload сообщения MsgServerInfo (сервер → агент).
type WireServerInfo struct {
	CommandsEnabled bool `json:"commands_enabled"`
}

// --- Streaming-терминал (PTY) ---

// WireTerminalOpen — создать PTY-сессию (admin → agent).
type WireTerminalOpen struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// WireTerminalInput — байты ввода в PTY (admin → agent).
type WireTerminalInput struct {
	SessionID string `json:"session_id"`
	DataB64   string `json:"data_b64"`
}

// WireTerminalResize — изменить размер PTY (admin → agent).
type WireTerminalResize struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// WireTerminalClose — закрыть PTY (admin → agent).
type WireTerminalClose struct {
	SessionID string `json:"session_id"`
}

// WireTerminalOutput — поток вывода PTY (agent → admin).
type WireTerminalOutput struct {
	SessionID string `json:"session_id"`
	DataB64   string `json:"data_b64"`
}

// WireTerminalExit — PTY завершён (agent → admin).
type WireTerminalExit struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
}

// --- Трансляция экрана (MJPEG) ---

// WireScreenStart — начать захват (admin → agent).
type WireScreenStart struct {
	SessionID string `json:"session_id"`
	FPS       int    `json:"fps"`
	Quality   int    `json:"quality"`
}

// WireScreenStop — остановить захват (admin → agent).
type WireScreenStop struct {
	SessionID string `json:"session_id"`
}

// WireScreenFrame — JPEG-кадр (agent → admin).
type WireScreenFrame struct {
	SessionID string `json:"session_id"`
	FrameB64  string `json:"frame_b64"`
	W         int    `json:"w"`
	H         int    `json:"h"`
}

// --- Скриншоты ---

// WireScreenshotSnap — сделать одиночный кадр (admin → agent).
type WireScreenshotSnap struct {
	SessionID string `json:"session_id"`
}

// WireScreenshotDone — кадр готов (agent → admin).
type WireScreenshotDone struct {
	SessionID string `json:"session_id"`
	FrameB64  string `json:"frame_b64"`
	W         int    `json:"w"`
	H         int    `json:"h"`
}

// Dispatcher координирует отправку команд и приём результатов.
type Dispatcher struct {
	store *store.Store
	hub   *hub.Hub
	log   *slog.Logger
}

// New создаёт диспетчер.
func New(s *store.Store, h *hub.Hub, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{store: s, hub: h, log: log}
}

// Dispatch ставит команду в очередь и (если устройство онлайн) отправляет её WS.
// Возвращает созданную команду и флаг delivered (true = отправлена мгновенно).
func (d *Dispatcher) Dispatch(ctx context.Context, deviceID, command string, timeoutSec int, createdBy string) (store.Command, bool, error) {
	// Проверяем глобальный тумблер.
	enabled, err := d.store.GetSetting(ctx, "commands_enabled", "1")
	if err != nil {
		return store.Command{}, false, err
	}
	if enabled == "0" {
		return store.Command{}, false, ErrCommandsDisabled
	}

	cmd := store.Command{
		ID:         newID(),
		DeviceID:   deviceID,
		Command:    command,
		TimeoutSec: timeoutSec,
		Status:     store.StatusQueued,
		CreatedBy:  createdBy,
	}
	if _, err := d.store.EnqueueCommand(ctx, cmd); err != nil {
		return store.Command{}, false, err
	}
	_ = d.store.AppendAudit(ctx, createdBy, "command.enqueue", deviceID, command)

	delivered := false
	if d.hub.IsOnline(deviceID) {
		env, err := d.sealCommand(ctx, cmd)
		if err != nil {
			d.log.Warn("seal command failed", "err", err, "device", deviceID)
		} else if sent := d.hub.SendToAgent(deviceID, env); sent {
			// Помечаем dispatched — агент уже увидит команду по WS.
			// Статус running агент установит неявно, забрав команду.
			delivered = true
		}
	}
	// Если не доставили мгновенно — останется queued, агент заберёт при следующем poll.
	return cmd, delivered, nil
}

// sealCommand шифрует команду ключом сессии устройства в envelope.
func (d *Dispatcher) sealCommand(ctx context.Context, cmd store.Command) (crypto.Envelope, error) {
	keyB64, err := d.store.GetSessionKey(ctx, cmd.DeviceID)
	if err != nil {
		return crypto.Envelope{}, err
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return crypto.Envelope{}, fmt.Errorf("commands: decode session key: %w", err)
	}
	sess, err := crypto.NewSession(key)
	if err != nil {
		return crypto.Envelope{}, err
	}
	return crypto.SealEnvelope(sess, crypto.MsgCommand, WireCommand{
		ID: cmd.ID, Command: cmd.Command, TimeoutSec: cmd.TimeoutSec,
	})
}

// SessionFor возвращает готовую сессию для устройства (используется transport-слоем).
func (d *Dispatcher) SessionFor(ctx context.Context, deviceID string) (*crypto.Session, error) {
	keyB64, err := d.store.GetSessionKey(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("commands: decode session key: %w", err)
	}
	return crypto.NewSession(key)
}

// SealFor шифрует произвольный payload под ключ сессии устройства и возвращает
// готовый envelope. Используется для streaming-сообщений (terminal/screen) —
// без записи в БД, в отличие от Dispatch. Публичный, чтобы api-слой мог звать.
func (d *Dispatcher) SealFor(ctx context.Context, deviceID, msgType string, payload any) (crypto.Envelope, error) {
	sess, err := d.SessionFor(ctx, deviceID)
	if err != nil {
		return crypto.Envelope{}, err
	}
	return crypto.SealEnvelope(sess, msgType, payload)
}

// SealForRaw шифрует уже готовые байты payload (например, сырой JSON от панели)
// под ключ устройства. Тип сообщения выносится в envelope.type, а payload
// отправляется как есть — агент дешифрует и сам парсит по типу.
func (d *Dispatcher) SealForRaw(ctx context.Context, deviceID, msgType string, rawPayload []byte) (crypto.Envelope, error) {
	sess, err := d.SessionFor(ctx, deviceID)
	if err != nil {
		return crypto.Envelope{}, err
	}
	ct, err := sess.Encrypt(rawPayload)
	if err != nil {
		return crypto.Envelope{}, err
	}
	return crypto.Envelope{Type: msgType, Data: base64.StdEncoding.EncodeToString(ct)}, nil
}

// HandleResult обрабатывает результат, пришедший от агента (WS или long-poll).
func (d *Dispatcher) HandleResult(ctx context.Context, deviceID string, r WireResult) error {
	status := store.StatusDone
	if r.Error != "" {
		status = store.StatusError
	}
	err := d.store.SaveResult(ctx, r.CommandID, status, store.Result{
		ExitCode:   r.ExitCode,
		StdoutB64:  r.StdoutB64,
		StderrB64:  r.StderrB64,
		FinishedAt: time.Now().UTC(),
		DurationMs: r.DurationMs,
	})
	if err != nil {
		return err
	}
	d.hub.Broadcast(hub.AdminEvent{
		Type: "command.result", DeviceID: deviceID,
		Payload: r,
	})
	_ = d.store.AppendAudit(ctx, deviceID, "command.result", r.CommandID, string(status))
	return nil
}

// FlushQueuedFor возвращает pending-команды для устройства при подключении/long-poll
// и sealing-их под ключ сессии в один batch envelope-ов.
func (d *Dispatcher) FlushQueuedFor(ctx context.Context, deviceID string) ([]crypto.Envelope, error) {
	cmds, err := d.store.DequeueCommands(ctx, deviceID, 32)
	if err != nil {
		return nil, err
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	sess, err := d.SessionFor(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]crypto.Envelope, 0, len(cmds))
	for _, c := range cmds {
		env, err := crypto.SealEnvelope(sess, crypto.MsgCommand, WireCommand{
			ID: c.ID, Command: c.Command, TimeoutSec: c.TimeoutSec,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, nil
}

// SetCommandsEnabled переключает глобальный тумблер диспетчеризации.
func (d *Dispatcher) SetCommandsEnabled(ctx context.Context, enabled bool) error {
	v := "0"
	if enabled {
		v = "1"
	}
	if err := d.store.SetSetting(ctx, "commands_enabled", v); err != nil {
		return err
	}
	_ = d.store.AppendAudit(ctx, "admin", "commands.toggle", "", v)
	d.hub.Broadcast(hub.AdminEvent{Type: "commands.toggle", Payload: enabled})
	return nil
}

// CommandsEnabled возвращает текущее состояние тумблера.
func (d *Dispatcher) CommandsEnabled(ctx context.Context) (bool, error) {
	v, err := d.store.GetSetting(ctx, "commands_enabled", "1")
	return v == "1", err
}

// ErrCommandsDisabled — диспетчеризация выключена глобально.
var ErrCommandsDisabled = fmt.Errorf("commands dispatch is globally disabled")
