package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nopass0/spider/server/internal/commands"
	"github.com/nopass0/spider/server/internal/crypto"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
	"nhooyr.io/websocket"
)

// --- Enrollment request/response ---

// enrollRequest — агент присылает свой публичный ключ X25519 + базовую инфо о системе.
type enrollRequest struct {
	Token     string                 `json:"token"`
	PublicKey string                 `json:"public_key"` // base64 X25519 публичный ключ агента
	System    commands.WireHeartbeat `json:"system"`
	AgentVer  string                 `json:"agent_version"`
}

// enrollResponse — сервер отдаёт device_id и общий симметричный ключ.
type enrollResponse struct {
	DeviceID string `json:"device_id"`
	KeyB64   string `json:"key"` // base64(симметричный ключ сессии)
}

// handleEnroll регистрирует новое устройство по одноразовому токену.
//
// Шаги:
//  1. Валидация токена (store.ConsumeEnrollment) — атомарно, с TTL.
//  2. Генерация device_id.
//  3. Получение симметричного ключа из enrollment-записи (уже созданного админом).
//  4. Сохранение устройства с сист.инфо.
//  5. Возврат device_id + ключа.
func (a *API) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := decodeJSON(r, &req, 64*1024); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "token and public_key are required")
		return
	}

	deviceID, err := crypto.RandomHex(16) // 32 hex-символа
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot generate device id")
		return
	}

	// Погашаем токен атомарно — получаем симметричный ключ сессии.
	keyB64, _, err := a.store.ConsumeEnrollment(r.Context(), req.Token, deviceID)
	if err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}

	// Сохраняем устройство с пришедшей сист.инфо.
	d := store.Device{
		DeviceID: deviceID,
		KeyB64:   keyB64,
		Hostname: req.System.Hostname,
		OS:       req.System.OS,
		Arch:     req.System.Arch,
		CPUBrand: req.System.CPUBrand,
		CPUCores: req.System.CPUCores,
		MemTotal: req.System.MemTotal,
		AgentVer: req.AgentVer,
		Online:   false,
	}
	if err := a.store.UpsertDevice(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), deviceID, "device.enroll", deviceID, req.System.Hostname)
	a.hub.Broadcast(hub.AdminEvent{Type: "device.enrolled", DeviceID: deviceID, Payload: req.System.Hostname})

	a.log.Info("device enrolled", "device_id", deviceID, "hostname", req.System.Hostname)
	writeJSON(w, http.StatusOK, enrollResponse{DeviceID: deviceID, KeyB64: keyB64})
}

// ===========================================================================
// Long-poll
// ===========================================================================

// longPollIn — batch результатов, который агент может прислать вместе с поллингом.
type longPollIn struct {
	DeviceID string                  `json:"device_id"`
	Results  []commands.WireResult   `json:"results,omitempty"`
	Heartbeat *commands.WireHeartbeat `json:"heartbeat,omitempty"`
}

// longPollOut — batch команд, отдаваемых агенту.
type longPollOut struct {
	Commands []crypto.Envelope         `json:"commands"`
	Info     *commands.WireServerInfo  `json:"info,omitempty"`
}

// handleLongPoll — fallback-транспорт: агент POST'ит/GET'ит, сервер держит
// соединение до появления команд или таймаута, затем отдаёт batch.
//
// Авторизация: device_id + ключ сессии. Агент шлёт результаты в шифрованном
// Envelope; сервер проверяет, что они расшифровываются ключом устройства.
func (a *API) handleLongPoll(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id required")
		return
	}
	sess, err := a.dispatcher.SessionFor(r.Context(), deviceID)
	if err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}

	// Если есть тело — это batch результатов от агента.
	if r.ContentLength > 0 && r.Method == http.MethodPost {
		var in longPollIn
		if err := decodeJSON(r, &in, 4<<20); err == nil && in.DeviceID == deviceID {
			for _, res := range in.Results {
				_ = a.dispatcher.HandleResult(r.Context(), deviceID, res)
			}
		}
	}

	// Отмечаем онлайн.
	_ = a.store.TouchDevice(r.Context(), deviceID, true)

	// Ждём появления команд до таймаута.
	timeout := a.cfg.LongPollTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	cmds := a.waitForCommands(ctx, deviceID)
	info := &commands.WireServerInfo{}
	enabled, _ := a.dispatcher.CommandsEnabled(r.Context())
	info.CommandsEnabled = enabled

	out := longPollOut{Commands: cmds, Info: info}
	writeJSON(w, http.StatusOK, out)
	_ = sess // ключ сессии используется для шифрования команд внутри FlushQueuedFor
}

// waitForCommands опрашивает очередь с короткими интервалами, пока не появятся
// команды или контекст не отменится. Простая и надёжная реализация long-poll.
func (a *API) waitForCommands(ctx context.Context, deviceID string) []crypto.Envelope {
	// Первая попытка сразу — обычно команда уже стоит в очереди.
	if batch, _ := a.dispatcher.FlushQueuedFor(ctx, deviceID); len(batch) > 0 {
		return batch
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if batch, _ := a.dispatcher.FlushQueuedFor(ctx, deviceID); len(batch) > 0 {
				return batch
			}
		}
	}
}

// ===========================================================================
// WebSocket (агент)
// ===========================================================================

// handleAgentWS — основной транспорт: постоянное WS-соединение агента.
func (a *API) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id required")
		return
	}
	sess, err := a.dispatcher.SessionFor(r.Context(), deviceID)
	if err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// CORS не нужен — агент не браузер; разрешаем любой origin.
		InsecureSkipVerify: true,
	})
	if err != nil {
		a.log.Warn("agent ws accept failed", "device_id", deviceID, "err", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")
	a.log.Info("agent ws connected", "device_id", deviceID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sink := newWSAgentSink(c, sess, a.log)
	unsub := a.hub.RegisterAgent(deviceID, sink)
	defer unsub()
	defer a.store.TouchDevice(context.Background(), deviceID, false)
	a.store.TouchDevice(ctx, deviceID, true)

	// Отправляем накопленные queued-команды при подключении.
	if batch, _ := a.dispatcher.FlushQueuedFor(ctx, deviceID); len(batch) > 0 {
		sink.writeEnvelopes(batch)
	}
	// Сразу шлём текущее состояние тумблера.
	if enabled, err := a.dispatcher.CommandsEnabled(ctx); err == nil {
		env, _ := crypto.SealEnvelope(sess, crypto.MsgServerInfo, commands.WireServerInfo{CommandsEnabled: enabled})
		sink.SendEnv(env)
	}

	// Читаем входящие сообщения (результаты, heartbeat) в отдельной горутине.
	// При ошибке чтения — отменяем ctx, что разблокирует писателя.
	go func() {
		a.readAgentLoop(ctx, deviceID, sess, c)
		cancel()
	}()

	// Писатель: отправляем то, что sink кладёт в канал, пока ctx жив.
	for {
		select {
		case <-ctx.Done():
			a.log.Info("agent ws writer done (ctx)", "device_id", deviceID)
			return
		case env, ok := <-sink.out:
			if !ok {
				a.log.Info("agent ws writer done (sink closed)", "device_id", deviceID)
				return
			}
			if err := writeWSJSON(ctx, c, env); err != nil {
				a.log.Info("agent ws writer done (write err)", "device_id", deviceID, "err", err)
				return
			}
		}
	}
}

// readAgentLoop читает шифрованные envelope-ы от агента и диспетчеризует их.
func (a *API) readAgentLoop(ctx context.Context, deviceID string, sess *crypto.Session, c *websocket.Conn) {
	for {
		var env crypto.Envelope
		if err := readWSJSON(ctx, c, &env); err != nil {
			a.log.Info("agent ws reader done", "device_id", deviceID, "err", err)
			return
		}
		switch env.Type {
		case crypto.MsgCommandResult:
			var res commands.WireResult
			if err := crypto.OpenEnvelope(sess, env, &res); err == nil {
				_ = a.dispatcher.HandleResult(ctx, deviceID, res)
			}
		case crypto.MsgHeartbeat:
			var hb commands.WireHeartbeat
			if err := crypto.OpenEnvelope(sess, env, &hb); err == nil {
				a.handleHeartbeat(ctx, deviceID, hb)
			}
		case crypto.MsgPing:
			pong, _ := crypto.SealEnvelope(sess, crypto.MsgPong, map[string]string{"t": "1"})
			_ = writeWSJSON(ctx, c, pong)
		case crypto.MsgTerminalOutput, crypto.MsgTerminalExit,
			crypto.MsgScreenFrame, crypto.MsgScreenshotDone:
			// Streaming-трафик: расшифровываем сырой payload и ретранслируем
			// только админам, подписанным на это устройство (через /admin/devices/{id}/stream).
			if raw, err := sess.DecryptEnvelope(env); err == nil {
				a.hub.SendToAdminsOf(deviceID, hub.AdminEvent{
					Type:     env.Type,
					DeviceID: deviceID,
					Payload:  json.RawMessage(raw),
				})
			}
		}
	}
}

// handleHeartbeat обновляет сист.инфо устройства из heartbeat-а.
func (a *API) handleHeartbeat(ctx context.Context, deviceID string, hb commands.WireHeartbeat) {
	d, err := a.store.GetDevice(ctx, deviceID)
	if err != nil {
		return
	}
	d.Hostname = orDefault(hb.Hostname, d.Hostname)
	d.OS = orDefault(hb.OS, d.OS)
	d.Arch = orDefault(hb.Arch, d.Arch)
	d.CPUBrand = orDefault(hb.CPUBrand, d.CPUBrand)
	if hb.CPUCores > 0 {
		d.CPUCores = hb.CPUCores
	}
	if hb.MemTotal > 0 {
		d.MemTotal = hb.MemTotal
	}
	d.AgentVer = orDefault(hb.AgentVer, d.AgentVer)
	d.Online = true
	_ = a.store.UpsertDevice(ctx, d)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// --- WS agent sink ---

// wsAgentSink реализует hub.AgentSink поверх WS-соединения.
type wsAgentSink struct {
	conn *websocket.Conn
	sess *crypto.Session
	out  chan crypto.Envelope
	log  *slog.Logger
	done chan struct{}
}

func newWSAgentSink(c *websocket.Conn, sess *crypto.Session, log *slog.Logger) *wsAgentSink {
	// Буфер 256: поток кадров экрана может быть плотным; не хотим дропать.
	s := &wsAgentSink{conn: c, sess: sess, log: log, out: make(chan crypto.Envelope, 256), done: make(chan struct{})}
	return s
}

// SendEnv кладёт envelope в исходящий канал (неблокирующе).
func (s *wsAgentSink) SendEnv(e crypto.Envelope) bool {
	select {
	case s.out <- e:
		return true
	default:
		return false
	}
}
func (s *wsAgentSink) Done() <-chan struct{} { return s.done }

// writeEnvelopes пытается записать несколько envelope-ов синхронно (при подключении).
func (s *wsAgentSink) writeEnvelopes(envs []crypto.Envelope) {
	for _, e := range envs {
		select {
		case s.out <- e:
		default:
		}
	}
}

// --- низкоуровневые WS-хелперы ---

func writeWSJSON(ctx context.Context, c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("ws marshal: %w", err)
	}
	return c.Write(ctx, websocket.MessageText, data)
}

func readWSJSON(ctx context.Context, c *websocket.Conn, v any) error {
	_, data, err := c.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
