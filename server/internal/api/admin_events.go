package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nopass0/spider/server/internal/hub"
	"nhooyr.io/websocket"
)

// b64 — короткий алиас для base64-кодирования (DRY).
func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// adminEvents — GET /admin/events (WebSocket) — стрим событий панели.
// Авторизация WS: Bearer ADMIN_KEY в Sec-WebSocket-Protocol (бразуер не шлёт
// кастомные заголовки для new WebSocket; query ненадёжен — некоторые reverse-
// proxy режут query-string на WS-upgrade). Формат subprotocol: "bearer.<key>".
// Для не-браузерных клиентов также принимается Authorization: Bearer.
func (a *API) adminEvents(w http.ResponseWriter, r *http.Request) {
	if !wsAuth(r, a.cfg.AdminKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Согласуем subprotocol bearer.* (обратная связь браузеру, что токен принят).
	subproto := wsBearerSubproto(r)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{originHost(a.cfg.PublicURL)},
		Subprotocols:   []string{subproto},
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

// wsAuth проверяет авторизацию для WS-эндпоинтов панели. Принимает:
//   - Authorization: Bearer <key> (для не-браузерных клиентов);
//   - Sec-WebSocket-Protocol: bearer.<key> (для браузеров — new WebSocket(url, ['bearer.<key>'])).
//
// Query-параметр намеренно НЕ используется: некоторые reverse-proxy (включая
// отдельные конфигурации Caddy) обрезают query-string на WS-upgrade.
func wsAuth(r *http.Request, expectedKey string) bool {
	if checkAdminToken(r, expectedKey) {
		return true
	}
	// Subprotocol bearer.<key> из Sec-WebSocket-Protocol (для браузеров).
	for _, proto := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range splitHeaderTokens(proto) {
			if len(p) > len("bearer.") && p[:len("bearer.")] == "bearer." {
				if constantTimeEqual(p[len("bearer."):], expectedKey) {
					return true
				}
			}
		}
	}
	return false
}

// wsBearerSubproto возвращает subprotocol bearer.* из запроса (для согласования
// с клиентом в AcceptOptions.Subprotocols) или "".
func wsBearerSubproto(r *http.Request) string {
	for _, proto := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range splitHeaderTokens(proto) {
			if len(p) > len("bearer.") && p[:len("bearer.")] == "bearer." {
				return p
			}
		}
	}
	return ""
}

// splitHeaderTokens разбирает значения заголовка, разделённые запятыми.
func splitHeaderTokens(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

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
