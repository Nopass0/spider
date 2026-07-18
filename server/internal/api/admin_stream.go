package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nopass0/spider/server/internal/hub"
	"nhooyr.io/websocket"
)

// adminStream — двунаправленный WS для streaming-сессий (terminal/screen).
//
// Поток от панели:
//   панель шлёт JSON {type, ...payload} без шифрования (это админ-канал под TLS+
//   Bearer-токеном) → сервер оборачивает в Envelope под ключ устройства →
//   hub.SendToAgent.
//
// Поток к панели:
//   агент шлёт terminal.output/screen.frame → readAgentLoop вызывает
//   hub.SendToAdminsOf → здесь подписанный wsAdminSink доставляет в WS панели.
//
// Авторизация: ?token=ADMIN_KEY (WS из браузера не шлёт кастомные заголовки).
func (a *API) adminStream(w http.ResponseWriter, r *http.Request) {
	if !checkAdminToken(r, a.cfg.AdminKey) && r.URL.Query().Get("token") != a.cfg.AdminKey {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	deviceID := r.PathValue("id")
	if _, err := a.store.GetDevice(r.Context(), deviceID); err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{originHost(a.cfg.PublicURL)},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Подписка на события устройства — receives terminal.output/screen.frame.
	sink := &wsAdminSink{conn: c, evs: make(chan hub.AdminEvent, 256)}
	unsubSub := a.hub.SubscribeAdminToDevice(deviceID, sink)
	defer unsubSub()

	// Ридер: входящие сообщения от панели → маршрутизация на агента.
	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			a.handleStreamInput(ctx, deviceID, data)
		}
	}()

	// Писатель: события из sink.evs → WS панели.
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sink.evs:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := c.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

// handleStreamInput разбирает сообщение панели и маршрутизирует его на агента.
// Формат входящего JSON: {"type": "<msg-type>", ...payload}.
// Сервер шифрует payload под ключ устройства и отправляет через hub.
func (a *API) handleStreamInput(ctx context.Context, deviceID string, data []byte) {
	// Сначала определим тип сообщения.
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil || head.Type == "" {
		return
	}

	// Шифруем весь payload как есть под ключ устройства и отправляем агенту.
	// Агент сам десериализует payload по типу.
	env, err := a.dispatcher.SealForRaw(ctx, deviceID, head.Type, data)
	if err != nil {
		a.log.Warn("stream seal failed", "device_id", deviceID, "type", head.Type, "err", err)
		return
	}
	if !a.hub.SendToAgent(deviceID, env) {
		// устройство не онлайн — игнорируем (стрим не persist-ится).
		a.log.Debug("stream send: device offline", "device_id", deviceID, "type", head.Type)
	}
}
