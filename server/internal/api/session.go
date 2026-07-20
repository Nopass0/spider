package api

import (
	"net/http"
)

// adminLogin — POST /admin/login. Проверяет ADMIN_KEY и ставит cookie
// spider_token, чтобы браузерные WebSocket (new WebSocket не умеет кастомные
// заголовки) могли авторизоваться — браузер автоматически шлёт куки.
//
// Cookie: HttpOnly (не виден JS, но шлётся на WS), SameSite=Lax, Secure (через
// Caddy — https). Без истечения = session cookie, живёт до закрытия вкладки.
// При logout панель зовёт DELETE /admin/login для очистки.
func (a *API) adminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &body, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !constantTimeEqual(body.Key, a.cfg.AdminKey) {
		writeError(w, http.StatusUnauthorized, "invalid key")
		return
	}
	// SameSite=Lax: WS-upgrade считается "same-site" навигацией для Lax,
	// но для надёжности через query/subprotocol нет — используем cookie с
	// SameSite=None;Secure, чтобы гарантированно шёлся на cross-origin WS.
	http.SetCookie(w, &http.Cookie{
		Name:     "spider_token",
		Value:    body.Key,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   86400 * 7, // 7 дней
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// adminLogout — DELETE /admin/login: очищает cookie.
func (a *API) adminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "spider_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
