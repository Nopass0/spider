package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/nopass0/spider/server/internal/hub"
	"nhooyr.io/websocket"
)

// b64 — короткий алиас для base64-кодирования (DRY).
func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// adminEvents — GET /admin/events (WebSocket) — стрим событий панели.
// Авторизация: Bearer ADMIN_KEY в query-параметре ?token= (WS не шлёт заголовки
// из браузера напрямую для EventSource; используем query для WS-клиента панели).
func (a *API) adminEvents(w http.ResponseWriter, r *http.Request) {
	if !checkAdminToken(r, a.cfg.AdminKey) && r.URL.Query().Get("token") != a.cfg.AdminKey {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{originHost(a.cfg.PublicURL)},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	sink := &wsAdminSink{conn: c, evs: make(chan hub.AdminEvent, 64)}
	unsub := a.hub.RegisterAdmin(sink)
	defer unsub()

	ctx := r.Context()
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

// wsAdminSink реализует hub.AdminSink поверх WS-соединения.
type wsAdminSink struct {
	conn *websocket.Conn
	evs  chan hub.AdminEvent
}

func (s *wsAdminSink) SendEvent(e hub.AdminEvent) bool {
	select {
	case s.evs <- e:
		return true
	default:
		return false
	}
}
func (s *wsAdminSink) Done() <-chan struct{} { return make(chan struct{}) }

// originHost извлекает хост из URL для OriginPatterns.
func originHost(publicURL string) string {
	// убираем схему
	for _, pref := range []string{"https://", "http://", "wss://", "ws://"} {
		if len(publicURL) > len(pref) && publicURL[:len(pref)] == pref {
			publicURL = publicURL[len(pref):]
			break
		}
	}
	// убираем путь/порт-хвост оставляем: Caddy всё равно терминирует
	for i, c := range publicURL {
		if c == '/' || c == ':' {
			return publicURL[:i]
		}
	}
	return publicURL
}
